package matrix

import (
	"sync"
	"time"
)

// RunStatus is the lifecycle state of a single benchmark run.
type RunStatus string

const (
	StatusQueued  RunStatus = "queued"
	StatusRunning RunStatus = "running"
	StatusSuccess RunStatus = "success"
	StatusFailed  RunStatus = "failed"
	StatusSkipped RunStatus = "skipped"
)

// StepStatus is the state of a single step within a run.
type StepStatus string

const (
	StepPending StepStatus = "pending"
	StepRunning StepStatus = "running"
	StepDone    StepStatus = "done"
	StepError   StepStatus = "error"
)

// StepInfo tracks one benchmark step (reset/deploy/loadgen/collect/upload).
type StepInfo struct {
	Name   string     `json:"name"`
	Status StepStatus `json:"status"`
}

// GlobalDefaults are applied to every run unless overridden per-run.
type GlobalDefaults struct {
	Concurrency   int    `yaml:"concurrency"    json:"concurrency"`
	DBURL         string `yaml:"db_url"         json:"db_url"`
	PrometheusURL string `yaml:"prometheus_url" json:"prometheus_url"`
	GCSBucket     string `yaml:"gcs_bucket"     json:"gcs_bucket"`
	// WorkerNode is the kubernetes.io/hostname value for nodeSelector (loadgen + controller).
	// Set to "" to skip node pinning.
	WorkerNode string `yaml:"worker_node" json:"worker_node"`
	// ScaleFactor is passed as --scale-factor to loadgen. 0 means auto-detect.
	ScaleFactor int `yaml:"scale_factor,omitempty" json:"scale_factor,omitempty"`
}

// RunSpec is the static definition persisted in matrix.yaml.
type RunSpec struct {
	ID          string   `yaml:"id"                    json:"id"`
	Config      string   `yaml:"config"                json:"config"`   // relative to REPO_ROOT
	Scenario    string   `yaml:"scenario"              json:"scenario"` // relative to REPO_ROOT
	Tags        []string `yaml:"tags,omitempty"        json:"tags,omitempty"`
	Concurrency int      `yaml:"concurrency,omitempty" json:"concurrency,omitempty"`
	WorkerNode  string   `yaml:"worker_node,omitempty" json:"worker_node,omitempty"`
	// ScaleFactor overrides defaults.scale_factor for this run. 0 means inherit.
	ScaleFactor int `yaml:"scale_factor,omitempty" json:"scale_factor,omitempty"`
}

// RunState is the runtime state, kept in memory only (not persisted to YAML).
type RunState struct {
	Status    RunStatus   `json:"status"`
	StartedAt *time.Time  `json:"started_at,omitempty"`
	EndedAt   *time.Time  `json:"ended_at,omitempty"`
	ErrMsg    string      `json:"error,omitempty"`
	GCSPath   string      `json:"gcs_path,omitempty"`
	Steps     []StepInfo  `json:"steps"`
}

// Run is the API view combining spec + runtime state.
type Run struct {
	RunSpec
	RunState
}

// EffectiveConcurrency returns the concurrency to use, falling back to defaults.
func (r RunSpec) EffectiveConcurrency(d GlobalDefaults) int {
	if r.Concurrency > 0 {
		return r.Concurrency
	}
	if d.Concurrency > 0 {
		return d.Concurrency
	}
	return 100
}

// EffectiveWorkerNode returns the worker node for nodeSelector, falling back to defaults.
func (r RunSpec) EffectiveWorkerNode(d GlobalDefaults) string {
	if r.WorkerNode != "" {
		return r.WorkerNode
	}
	return d.WorkerNode
}

// EffectiveScaleFactor returns the --scale-factor to pass to loadgen.
// 0 means let loadgen auto-detect from the DB.
func (r RunSpec) EffectiveScaleFactor(d GlobalDefaults) int {
	if r.ScaleFactor > 0 {
		return r.ScaleFactor
	}
	return d.ScaleFactor
}

// MatrixFile is the on-disk schema of matrix.yaml.
type MatrixFile struct {
	Defaults GlobalDefaults `yaml:"defaults" json:"defaults"`
	Runs     []RunSpec      `yaml:"runs"     json:"runs"`
}

// Store holds the matrix definition and in-memory runtime state with thread safety.
type Store struct {
	mu       sync.RWMutex
	file     MatrixFile
	states   map[string]*RunState // key: run ID
	order    []string             // ordered run IDs (supports reorder)
	filePath string
}
