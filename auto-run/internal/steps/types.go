// Package steps contains the five benchmark pipeline steps:
// reset-cluster → deploy-controller → run-loadgen → collect-metrics → upload-gcs
package steps

import (
	"fmt"
	"log/slog"
	"path/filepath"

	"github.com/armistcxy/kltn/auto-run/internal/filestore"
	"github.com/armistcxy/kltn/auto-run/internal/matrix"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// LogFunc is called with every log line produced by a step.
type LogFunc func(line string)

// RunContext carries everything a step needs to execute a single benchmark run.
type RunContext struct {
	RunSpec       matrix.RunSpec
	Defaults      matrix.GlobalDefaults
	SessionID     string // e.g. "20260406-143000"
	RepoRoot      string // absolute path to repo root
	ResultsDir    string // local staging dir, e.g. /results/<run-id>
	K8sClient     client.Client
	FileStore     *filestore.Store // nil-safe: falls back to repo path
	PrometheusURL string
	Log           LogFunc
}

// Logf formats and emits a log line through rc.Log.
func (rc *RunContext) Logf(format string, args ...any) {
	rc.Log(fmt.Sprintf(format, args...))
}

// ConfigPath returns the absolute path to the scale-controller config file.
// Uploaded files (in filestore) take priority over repo-relative paths.
func (rc *RunContext) ConfigPath() string {
	if rc.FileStore != nil {
		return rc.FileStore.Resolve(filestore.CategoryConfigs, rc.RunSpec.Config, rc.RepoRoot)
	}
	return filepath.Join(rc.RepoRoot, rc.RunSpec.Config)
}

// ScenarioPath returns the absolute path to the loadgen scenario file.
// Uploaded files (in filestore) take priority over repo-relative paths.
func (rc *RunContext) ScenarioPath() string {
	if rc.FileStore != nil {
		return rc.FileStore.Resolve(filestore.CategoryScenarios, rc.RunSpec.Scenario, rc.RepoRoot)
	}
	return filepath.Join(rc.RepoRoot, rc.RunSpec.Scenario)
}

// EffectiveConcurrency returns the concurrency to use for this run.
func (rc *RunContext) EffectiveConcurrency() int {
	return rc.RunSpec.EffectiveConcurrency(rc.Defaults)
}

// EffectiveWorkerNode returns the worker node for nodeSelector (empty = no pin).
func (rc *RunContext) EffectiveWorkerNode() string {
	return rc.RunSpec.EffectiveWorkerNode(rc.Defaults)
}

// EffectiveScaleFactor returns the --scale-factor for loadgen (0 = auto-detect).
func (rc *RunContext) EffectiveScaleFactor() int {
	return rc.RunSpec.EffectiveScaleFactor(rc.Defaults)
}

// EffectiveGCSBucket returns the GCS bucket from defaults.
func (rc *RunContext) EffectiveGCSBucket() string {
	return rc.Defaults.GCSBucket
}

// logStep emits a formatted step header.
func logStep(log LogFunc, step, msg string) {
	log(fmt.Sprintf("[%s] %s", step, msg))
}

type logWriter struct{ log LogFunc }

func (w logWriter) Write(p []byte) (int, error) {
	w.log(string(p))
	return len(p), nil
}

// NewSlogLogger creates a slog.Logger that routes to a LogFunc.
func NewSlogLogger(log LogFunc) *slog.Logger {
	return slog.New(slog.NewTextHandler(logWriter{log: log}, nil))
}
