package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"

	"github.com/armistcxy/kltn/auto-run/internal/api"
	"github.com/armistcxy/kltn/auto-run/internal/bus"
	"github.com/armistcxy/kltn/auto-run/internal/filestore"
	"github.com/armistcxy/kltn/auto-run/internal/matrix"
	"github.com/armistcxy/kltn/auto-run/internal/orchestrator"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrlconfig "sigs.k8s.io/controller-runtime/pkg/client/config"
)

func main() {
	matrixFile  := flag.String("matrix",   envOrDefault("MATRIX_FILE", "/config/matrix.yaml"), "Path to matrix.yaml")
	repoRoot    := flag.String("repo",     envOrDefault("REPO_ROOT", "/repo"), "Path to repository root")
	resultsBase := flag.String("results",  envOrDefault("RESULTS_DIR", "/results"), "Base dir for local staging")
	uploadsDir  := flag.String("uploads",  envOrDefault("UPLOADS_DIR", "/uploads"), "Base dir for uploaded files")
	addr        := flag.String("addr",     ":8080", "HTTP listen address")
	uiDir       := flag.String("ui",       envOrDefault("UI_DIR", "/ui"), "Directory containing the Web UI files")
	flag.Parse()

	slog.Info("auto-run server starting",
		"matrix", *matrixFile,
		"repo", *repoRoot,
		"addr", *addr,
	)

	// ── Matrix store ───────────────────────────────────────────
	store, err := matrix.NewStore(*matrixFile)
	if err != nil {
		slog.Error("failed to load matrix", "err", err)
		os.Exit(1)
	}
	slog.Info("matrix loaded", "runs", len(store.Runs()))

	// ── Kubernetes clients ─────────────────────────────────────
	if err := cnpgv1.AddToScheme(scheme.Scheme); err != nil {
		slog.Error("register CNPG scheme", "err", err)
		os.Exit(1)
	}
	k8sCfg, err := ctrlconfig.GetConfig()
	if err != nil {
		slog.Error("load kubeconfig", "err", err)
		os.Exit(1)
	}
	k8sClient, err := client.New(k8sCfg, client.Options{Scheme: scheme.Scheme})
	if err != nil {
		slog.Error("create k8s client", "err", err)
		os.Exit(1)
	}
	clientset, err := kubernetes.NewForConfig(k8sCfg)
	if err != nil {
		slog.Error("create clientset", "err", err)
		os.Exit(1)
	}

	ctx := context.Background()

	// Sync matrix from ConfigMap (overrides local file if a previous run saved one).
	if err := store.SyncFromConfigMap(ctx, k8sClient); err != nil {
		slog.Warn("sync from configmap failed (continuing with local file)", "err", err)
	}

	// ── File store ─────────────────────────────────────────────
	fs := filestore.New(filepath.Clean(*uploadsDir), k8sClient)
	if err := fs.Init(); err != nil {
		slog.Error("init file store", "err", err)
		os.Exit(1)
	}
	// Restore uploaded files from ConfigMaps (pod restart recovery).
	if err := fs.RestoreFromConfigMaps(ctx); err != nil {
		slog.Warn("restore uploaded files failed", "err", err)
	}

	// ── Ensure results dir ─────────────────────────────────────
	if err := os.MkdirAll(*resultsBase, 0o755); err != nil {
		slog.Error("create results dir", "err", err)
		os.Exit(1)
	}

	// ── Event bus + Orchestrator ───────────────────────────────
	b := bus.New()
	orch := orchestrator.New(store, b, k8sClient, clientset,
		filepath.Clean(*repoRoot),
		filepath.Clean(*resultsBase),
		fs,
	)

	// ── HTTP server ────────────────────────────────────────────
	mux := http.NewServeMux()
	api.New(mux, store, orch, b, k8sClient, fs, *uiDir)

	srv := &http.Server{
		Addr:         *addr,
		Handler:      mux,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 0, // SSE streams need no write timeout
		IdleTimeout:  120 * time.Second,
	}

	// ── Graceful shutdown ──────────────────────────────────────
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		slog.Info("HTTP server listening", "addr", *addr)
		fmt.Printf("\n  Web UI -> http://localhost%s\n\n", *addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("HTTP server", "err", err)
			os.Exit(1)
		}
	}()

	<-quit
	slog.Info("shutting down…")
	if orch.IsRunning() {
		orch.Send(orchestrator.SignalStop)
	}
	shutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutCtx)
	slog.Info("bye")
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
