package scale

import (
	"context"
	"time"
)

// MetricsSnapshot holds a snapshot of various metrics at a specific time.
type MetricsSnapshot struct {
	At time.Time

	// Per pod metrics
	BackendsByPod map[string]float64
	CPUByPod      map[string]float64
	MemoryByPod   map[string]float64
	TPSByPod      map[string]float64

	// Aggregate metrics
	TotalBackends float64
	AvgCPU        float64
	MaxCPU        float64
	AvgMemory     float64
	MaxMemory     float64
	TotalTPS      float64
}

// ScaleActionType describes what scaling action should be taken.
type ScaleActionType string

const (
	ScaleNone ScaleActionType = "none"
	ScaleUp   ScaleActionType = "scale_up"
	ScaleDown ScaleActionType = "scale_down"
)

// ScaleDecision is the output of Decide().
type ScaleDecision struct {
	Action ScaleActionType

	// What to change
	TargetInstances int

	// Human-readable explanation (useful for logs/events)
	Reason string
}

// MetricsObserver fetches metrics from Prometheus (CNPG exporter)
// or directly from PostgreSQL.
type MetricsObserver interface {
	Observe(ctx context.Context) (*MetricsSnapshot, error)
}

type Config struct {
	// Min/max guardrails
	MinInstances int
	MaxInstances int

	// Thresholds
	BackendsScaleUpThreshold   float64 // example: 80 connections
	BackendsScaleDownThreshold float64 // example: 20 connections

	CPUScaleUpThreshold   float64 // example: 1.2 cores
	CPUScaleDownThreshold float64 // example: 0.2 cores

	MemoryScaleUpThreshold   float64 // example: 1.5 GB
	MemoryScaleDownThreshold float64 // example: 0.5 GB

	TPSScaleUpThreshold   float64 // example: 1000 tps
	TPSScaleDownThreshold float64 // example: 100 tps

	// Cooldown: prevent flapping
	Cooldown time.Duration

	// Poll interval (how often Run() loops)
	PollInterval time.Duration
}
