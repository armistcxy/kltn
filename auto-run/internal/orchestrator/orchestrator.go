// Package orchestrator runs benchmark runs sequentially from the matrix store.
package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/armistcxy/kltn/auto-run/internal/bus"
	"github.com/armistcxy/kltn/auto-run/internal/filestore"
	"github.com/armistcxy/kltn/auto-run/internal/matrix"
	"github.com/armistcxy/kltn/auto-run/internal/steps"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Signal controls the orchestrator loop.
type Signal int

const (
	SignalStart Signal = iota
	SignalPause        // finish current run then pause
	SignalStop         // cancel current run and stop
)

// Orchestrator runs matrix runs sequentially and broadcasts events via the Bus.
type Orchestrator struct {
	store       *matrix.Store
	bus         *bus.Bus
	k8s         client.Client
	clientset   kubernetes.Interface
	repoRoot    string
	resultsBase string
	fileStore   *filestore.Store

	mu         sync.Mutex
	running    bool
	sessionID  string
	controlCh  chan Signal
	pauseAfter bool
	cancelRun  context.CancelFunc
}

// New creates an Orchestrator.
func New(
	store *matrix.Store,
	b *bus.Bus,
	k8s client.Client,
	clientset kubernetes.Interface,
	repoRoot string,
	resultsBase string,
	fs *filestore.Store,
) *Orchestrator {
	return &Orchestrator{
		store:       store,
		bus:         b,
		k8s:         k8s,
		clientset:   clientset,
		repoRoot:    repoRoot,
		resultsBase: resultsBase,
		fileStore:   fs,
		controlCh:   make(chan Signal, 4),
	}
}

// Send sends a control signal.
func (o *Orchestrator) Send(sig Signal) {
	o.controlCh <- sig
}

// IsRunning returns true if the orchestrator loop is active.
func (o *Orchestrator) IsRunning() bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.running
}

// Start launches the orchestrator loop in a background goroutine.
// Safe to call multiple times; does nothing if already running.
func (o *Orchestrator) Start() {
	o.mu.Lock()
	if o.running {
		o.mu.Unlock()
		return
	}
	o.running = true
	o.sessionID = time.Now().UTC().Format("20060102-150405")
	o.mu.Unlock()
	go o.loop()
}

func (o *Orchestrator) loop() {
	defer func() {
		o.mu.Lock()
		o.running = false
		o.mu.Unlock()
		o.bus.Publish(bus.Event{Type: "status", Payload: "idle"})
	}()

	o.bus.Publish(bus.Event{Type: "status", Payload: "running"})

	for {
		// Drain any pending signals.
		select {
		case sig := <-o.controlCh:
			if sig == SignalStop {
				o.log("orchestrator stopped")
				return
			}
		default:
		}

		runID := o.store.NextQueued()
		if runID == "" {
			o.log("all runs complete")
			return
		}

		if err := o.executeRun(runID); err != nil {
			o.log("run %s failed: %v", runID, err)
		}

		// Check for pause-after signal.
		o.mu.Lock()
		pause := o.pauseAfter
		o.pauseAfter = false
		o.mu.Unlock()
		if pause {
			o.log("paused after run %s", runID)
			o.bus.Publish(bus.Event{Type: "status", Payload: "paused"})
			// Block until Start or Stop signal.
			for sig := range o.controlCh {
				if sig == SignalStart {
					o.bus.Publish(bus.Event{Type: "status", Payload: "running"})
					break
				}
				if sig == SignalStop {
					return
				}
			}
		}
	}
}

func (o *Orchestrator) executeRun(runID string) error {
	spec, ok := o.store.GetSpec(runID)
	if !ok {
		return fmt.Errorf("run %q not found in store", runID)
	}
	defs := o.store.Defaults()

	resultsDir := filepath.Join(o.resultsBase, runID)
	if err := os.MkdirAll(resultsDir, 0o755); err != nil {
		return fmt.Errorf("mkdir results: %w", err)
	}

	logFn := func(line string) {
		o.bus.PublishLog(runID, line)
	}

	rc := &steps.RunContext{
		RunSpec:       spec,
		Defaults:      defs,
		SessionID:     o.sessionID,
		RepoRoot:      o.repoRoot,
		ResultsDir:    resultsDir,
		K8sClient:     o.k8s,
		FileStore:     o.fileStore,
		PrometheusURL: defs.PrometheusURL,
		Log:           logFn,
	}

	now := time.Now()
	state := matrix.RunState{
		Status:    matrix.StatusRunning,
		StartedAt: &now,
		Steps:     buildSteps(),
	}
	o.store.SetState(runID, state)
	o.bus.PublishStatus(runID, string(matrix.StatusRunning))

	ctx, cancel := context.WithCancel(context.Background())
	o.mu.Lock()
	o.cancelRun = cancel
	o.mu.Unlock()
	defer cancel()

	// --- step runner helper ---
	runStep := func(name string, fn func() error) error {
		o.store.SetStepStatus(runID, name, matrix.StepRunning)
		o.bus.PublishStep(runID, name, string(matrix.StepRunning))
		if err := fn(); err != nil {
			o.store.SetStepStatus(runID, name, matrix.StepError)
			o.bus.PublishStep(runID, name, string(matrix.StepError))
			return err
		}
		o.store.SetStepStatus(runID, name, matrix.StepDone)
		o.bus.PublishStep(runID, name, string(matrix.StepDone))
		return nil
	}

	var startTS, endTS time.Time
	var runErr error
	var replicaSeconds float64

	// 1. Reset cluster
	if runErr = runStep("reset-cluster", func() error {
		return steps.ResetCluster(ctx, rc)
	}); runErr != nil {
		goto finish
	}

	// 2. Deploy controller
	if runErr = runStep("deploy-controller", func() error {
		return steps.DeployController(ctx, rc)
	}); runErr != nil {
		goto finish
	}

	// 3. Run loadgen
	startTS = time.Now()
	if runErr = runStep("run-loadgen", func() error {
		return steps.RunLoadgen(ctx, rc, o.clientset)
	}); runErr != nil {
		endTS = time.Now()
		goto teardown
	}
	endTS = time.Now()

	// 4. Collect metrics
	_ = runStep("collect-metrics", func() error {
		if err := steps.CollectMetrics(ctx, rc, o.clientset, startTS, endTS); err != nil {
			return err
		}
		rs, err := steps.ComputeReplicaSeconds(resultsDir)
		if err != nil {
			logFn(fmt.Sprintf("[collect-metrics] warn: replica_seconds: %v", err))
		} else {
			replicaSeconds = rs
		}
		return nil
	})

teardown:
	steps.TeardownController(ctx, rc)

	// 5. Take backup (best-effort)
	_ = runStep("take-backup", func() error {
		return steps.TakeBackup(ctx, rc)
	})

	// 6. Upload GCS (best-effort, even on failure)
	if defs.GCSBucket != "" {
		_ = runStep("upload-gcs", func() error {
			meta := steps.Meta{
				RunID:          runID,
				SessionID:      o.sessionID,
				ConfigFile:     spec.Config,
				ScenarioFile:   spec.Scenario,
				StartTS:        startTS.Unix(),
				EndTS:          endTS.Unix(),
				DurationS:      int64(endTS.Sub(startTS).Seconds()),
				GitCommit:      gitCommit(o.repoRoot),
				Concurrency:    rc.EffectiveConcurrency(),
				WorkerNode:     rc.EffectiveWorkerNode(),
				DBURL:          redactPassword(defs.DBURL),
				ReplicaSeconds: replicaSeconds,
			}
			if runErr != nil {
				meta.Status = "FAILED"
			} else {
				meta.Status = "SUCCESS"
			}
			if err := steps.WriteMeta(resultsDir, meta); err != nil {
				logFn(fmt.Sprintf("[upload-gcs] warn: write meta.json: %v", err))
			}
			gcsPath, err := steps.UploadGCS(ctx, rc, o.sessionID)
			if err != nil {
				return err
			}
			// Update state with GCS path.
			if st, ok := o.store.GetState(runID); ok {
				st.GCSPath = gcsPath
				o.store.SetState(runID, st)
			}
			return nil
		})
	}

finish:
	end := time.Now()
	finalState, _ := o.store.GetState(runID)
	finalState.EndedAt = &end
	if runErr != nil {
		finalState.Status = matrix.StatusFailed
		finalState.ErrMsg = runErr.Error()
	} else {
		finalState.Status = matrix.StatusSuccess
	}
	o.store.SetState(runID, finalState)
	o.bus.PublishStatus(runID, string(finalState.Status))
	return runErr
}

// PauseAfterCurrent sets a flag to pause after the current run finishes.
func (o *Orchestrator) PauseAfterCurrent() {
	o.mu.Lock()
	o.pauseAfter = true
	o.mu.Unlock()
}

// StopCurrent cancels the currently executing run.
func (o *Orchestrator) StopCurrent() {
	o.mu.Lock()
	cancel := o.cancelRun
	o.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (o *Orchestrator) log(format string, args ...any) {
	o.bus.PublishLog("", fmt.Sprintf("[orchestrator] "+format, args...))
}

func buildSteps() []matrix.StepInfo {
	names := []string{"reset-cluster", "deploy-controller", "run-loadgen", "collect-metrics", "take-backup", "upload-gcs"}
	steps := make([]matrix.StepInfo, len(names))
	for i, n := range names {
		steps[i] = matrix.StepInfo{Name: n, Status: matrix.StepPending}
	}
	return steps
}

func gitCommit(repoRoot string) string {
	cmd := exec.Command("git", "-C", repoRoot, "rev-parse", "--short", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(out))
}

func redactPassword(dsn string) string {
	// postgres://user:password@host/db → postgres://user:***@host/db
	if i := strings.Index(dsn, "://"); i >= 0 {
		rest := dsn[i+3:]
		if at := strings.LastIndex(rest, "@"); at >= 0 {
			creds := rest[:at]
			if colon := strings.Index(creds, ":"); colon >= 0 {
				return dsn[:i+3] + creds[:colon] + ":***@" + rest[at+1:]
			}
		}
	}
	return dsn
}

// ControlAction is the JSON body for POST /api/control.
type ControlAction struct {
	Action string `json:"action"` // start | pause | stop | retry
}

// ApplyControl applies a control action to the orchestrator.
func (o *Orchestrator) ApplyControl(action ControlAction) error {
	switch action.Action {
	case "start":
		if !o.IsRunning() {
			o.Start()
		} else {
			o.Send(SignalStart)
		}
	case "pause":
		o.PauseAfterCurrent()
	case "stop":
		o.StopCurrent()
		o.Send(SignalStop)
	case "retry":
		o.store.ResetQueued()
		if !o.IsRunning() {
			o.Start()
		}
	default:
		return fmt.Errorf("unknown action %q", action.Action)
	}
	return nil
}

// SessionID returns the current session ID.
func (o *Orchestrator) SessionID() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.sessionID
}

// marshalMeta is a helper used in tests.
func marshalMeta(m steps.Meta) ([]byte, error) {
	return json.Marshal(m)
}
