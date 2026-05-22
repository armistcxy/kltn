// Package api implements the HTTP REST API and SSE log streaming for the
// auto-run benchmark harness.
package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/armistcxy/kltn/auto-run/internal/bus"
	"github.com/armistcxy/kltn/auto-run/internal/filestore"
	"github.com/armistcxy/kltn/auto-run/internal/matrix"
	"github.com/armistcxy/kltn/auto-run/internal/orchestrator"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const maxUploadSize = 1 << 20 // 1 MiB per file

// Handler wires together the matrix store, orchestrator, and event bus.
type Handler struct {
	store *matrix.Store
	orch  *orchestrator.Orchestrator
	bus   *bus.Bus
	k8s   client.Client
	files *filestore.Store
}

// New creates a Handler and registers all routes on mux.
func New(
	mux *http.ServeMux,
	store *matrix.Store,
	orch *orchestrator.Orchestrator,
	b *bus.Bus,
	k8s client.Client,
	fs *filestore.Store,
	uiDir string,
) *Handler {
	h := &Handler{store: store, orch: orch, bus: b, k8s: k8s, files: fs}

	// Static UI
	mux.Handle("/", http.FileServer(http.Dir(uiDir)))

	// Matrix
	mux.HandleFunc("GET /api/matrix", h.getMatrix)
	mux.HandleFunc("PUT /api/matrix", h.putMatrix)

	// Per-run
	mux.HandleFunc("PATCH /api/runs/{id}", h.patchRun)
	mux.HandleFunc("POST /api/runs/{id}/move", h.moveRun)
	mux.HandleFunc("DELETE /api/runs/{id}", h.deleteRun)
	mux.HandleFunc("GET /api/runs/{id}/logs", h.streamLogs)

	// Files (uploaded configs + scenarios)
	// GET  /api/files/{category}           -> list files
	// POST /api/files/{category}           -> upload (multipart or raw body)
	// GET  /api/files/{category}/{name}    -> get content
	// DELETE /api/files/{category}/{name}  -> delete
	mux.HandleFunc("GET /api/files/{category}", h.listFiles)
	mux.HandleFunc("POST /api/files/{category}", h.uploadFile)
	mux.HandleFunc("GET /api/files/{category}/{name}", h.getFile)
	mux.HandleFunc("DELETE /api/files/{category}/{name}", h.deleteFile)

	// Control
	mux.HandleFunc("POST /api/control", h.postControl)

	// Settings (global defaults)
	mux.HandleFunc("GET /api/settings", h.getSettings)
	mux.HandleFunc("PUT /api/settings", h.putSettings)

	// Status
	mux.HandleFunc("GET /api/status", h.getStatus)

	return h
}

// ---- Matrix ----------------------------------------------------------------

func (h *Handler) getMatrix(w http.ResponseWriter, r *http.Request) {
	data, err := h.store.MatrixJSON()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonResponse(w, json.RawMessage(data))
}

func (h *Handler) putMatrix(w http.ResponseWriter, r *http.Request) {
	var mf matrix.MatrixFile
	if err := json.NewDecoder(r.Body).Decode(&mf); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	if err := validateMatrix(mf); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	h.store.ReplaceMatrix(mf)
	persistMatrix(h.store, h.k8s, r)
	jsonResponse(w, map[string]string{"status": "ok"})
}

// ---- Per-run ---------------------------------------------------------------

func (h *Handler) patchRun(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var spec matrix.RunSpec
	if err := json.NewDecoder(r.Body).Decode(&spec); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	spec.ID = id // ensure ID consistency
	if err := h.store.UpdateSpec(id, spec); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	persistMatrix(h.store, h.k8s, r)
	jsonResponse(w, map[string]string{"status": "ok"})
}

func (h *Handler) moveRun(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var body struct {
		After string `json:"after"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if err := h.store.MoveAfter(id, body.After); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	persistMatrix(h.store, h.k8s, r)
	jsonResponse(w, map[string]string{"status": "ok"})
}

func (h *Handler) deleteRun(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := h.store.DeleteRun(id); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	persistMatrix(h.store, h.k8s, r)
	jsonResponse(w, map[string]string{"status": "ok"})
}

// ---- Log streaming (SSE) ---------------------------------------------------

func (h *Handler) streamLogs(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	ch := h.bus.Subscribe(id)
	defer h.bus.Unsubscribe(id, ch)

	// Send keepalive comments every 15s so the browser doesn't time out.
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			fmt.Fprintf(w, ": keepalive\n\n")
			flusher.Flush()
		case e, ok := <-ch:
			if !ok {
				return
			}
			data, _ := json.Marshal(e)
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		}
	}
}

// ---- Control ---------------------------------------------------------------

func (h *Handler) postControl(w http.ResponseWriter, r *http.Request) {
	var action orchestrator.ControlAction
	if err := json.NewDecoder(r.Body).Decode(&action); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if err := h.orch.ApplyControl(action); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	jsonResponse(w, map[string]string{"status": "ok"})
}

// ---- Settings --------------------------------------------------------------

func (h *Handler) getSettings(w http.ResponseWriter, r *http.Request) {
	jsonResponse(w, h.store.Defaults())
}

func (h *Handler) putSettings(w http.ResponseWriter, r *http.Request) {
	var d matrix.GlobalDefaults
	if err := json.NewDecoder(r.Body).Decode(&d); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	h.store.UpdateDefaults(d)
	persistMatrix(h.store, h.k8s, r)
	jsonResponse(w, map[string]string{"status": "ok"})
}

// ---- Status ----------------------------------------------------------------

func (h *Handler) getStatus(w http.ResponseWriter, r *http.Request) {
	jsonResponse(w, map[string]any{
		"running":    h.orch.IsRunning(),
		"session_id": h.orch.SessionID(),
	})
}

// ---- Helpers ---------------------------------------------------------------

func jsonResponse(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	_ = json.NewEncoder(w).Encode(v)
}

func validateMatrix(mf matrix.MatrixFile) error {
	seen := map[string]bool{}
	for _, r := range mf.Runs {
		if r.ID == "" {
			return fmt.Errorf("run ID must not be empty")
		}
		if seen[r.ID] {
			return fmt.Errorf("duplicate run ID: %q", r.ID)
		}
		seen[r.ID] = true
		if r.Config == "" {
			return fmt.Errorf("run %q: config must not be empty", r.ID)
		}
		if r.Scenario == "" {
			return fmt.Errorf("run %q: scenario must not be empty", r.ID)
		}
	}
	return nil
}

func persistMatrix(store *matrix.Store, k8s client.Client, r *http.Request) {
	// Best-effort: persist to ConfigMap in background.
	go func() {
		ctx := r.Context()
		if k8s != nil {
			_ = store.PersistToConfigMap(ctx, k8s)
		}
		_ = store.SaveToFile()
	}()
}

// AddRun is an extra endpoint for adding a single new run via UI.
func (h *Handler) addRun(w http.ResponseWriter, r *http.Request) {
	var spec matrix.RunSpec
	if err := json.NewDecoder(r.Body).Decode(&spec); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if spec.ID == "" || spec.Config == "" || spec.Scenario == "" {
		http.Error(w, "id, config, scenario are required", http.StatusBadRequest)
		return
	}
	mf := h.store.ToMatrixFile()
	for _, r := range mf.Runs {
		if r.ID == spec.ID {
			http.Error(w, fmt.Sprintf("run %q already exists", spec.ID), http.StatusConflict)
			return
		}
	}
	mf.Runs = append(mf.Runs, spec)
	h.store.ReplaceMatrix(mf)
	persistMatrix(h.store, h.k8s, r)
	w.WriteHeader(http.StatusCreated)
	jsonResponse(w, map[string]string{"status": "created"})
}

// ---- Files -----------------------------------------------------------------

func (h *Handler) listFiles(w http.ResponseWriter, r *http.Request) {
	category := r.PathValue("category")
	files, err := h.files.List(category)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	jsonResponse(w, files)
}

// uploadFile accepts either:
//   - multipart/form-data with field "file"
//   - raw request body (Content-Type: application/octet-stream or text/plain)
//
// The filename comes from the query param ?name=foo.yaml or the multipart filename.
func (h *Handler) uploadFile(w http.ResponseWriter, r *http.Request) {
	category := r.PathValue("category")
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadSize)

	var name string
	var content []byte

	ct := r.Header.Get("Content-Type")
	if strings.HasPrefix(ct, "multipart/form-data") {
		if err := r.ParseMultipartForm(maxUploadSize); err != nil {
			http.Error(w, "parse multipart: "+err.Error(), http.StatusBadRequest)
			return
		}
		f, header, err := r.FormFile("file")
		if err != nil {
			http.Error(w, "missing field 'file': "+err.Error(), http.StatusBadRequest)
			return
		}
		defer f.Close()
		name = filepath.Base(header.Filename)
		content, err = io.ReadAll(f)
		if err != nil {
			http.Error(w, "read file: "+err.Error(), http.StatusInternalServerError)
			return
		}
	} else {
		// Raw body upload; filename must come from ?name=
		name = r.URL.Query().Get("name")
		var err error
		content, err = io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "read body: "+err.Error(), http.StatusInternalServerError)
			return
		}
	}

	if err := h.files.Save(r.Context(), category, name, content); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusCreated)
	jsonResponse(w, map[string]string{"name": name, "category": category, "status": "uploaded"})
}

func (h *Handler) getFile(w http.ResponseWriter, r *http.Request) {
	category := r.PathValue("category")
	name := r.PathValue("name")
	content, err := h.files.Load(category, name)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	_, _ = w.Write(content)
}

func (h *Handler) deleteFile(w http.ResponseWriter, r *http.Request) {
	category := r.PathValue("category")
	name := r.PathValue("name")
	if err := h.files.Delete(r.Context(), category, name); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	jsonResponse(w, map[string]string{"status": "deleted"})
}

// ServeHTTP implements http.Handler so Handler can be used directly.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// CORS preflight
	if r.Method == http.MethodOptions {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		w.WriteHeader(http.StatusNoContent)
		return
	}
	// Strip trailing slash for API routes.
	if strings.HasPrefix(r.URL.Path, "/api/") && len(r.URL.Path) > 5 && r.URL.Path[len(r.URL.Path)-1] == '/' {
		r.URL.Path = r.URL.Path[:len(r.URL.Path)-1]
	}
}
