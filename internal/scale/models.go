package scale

import (
	"context"
	"time"
)

// MetricsSnapshot holds a point-in-time snapshot of all configured metrics.
type MetricsSnapshot struct {
	At     time.Time
	Values map[string]float64 // metricName → scalar value
}

// MetricSpec defines one metric to observe and how it drives scaling decisions.
type MetricSpec struct {
	// Name is the human-readable identifier, used as the key in MetricsSnapshot.Values.
	Name string `yaml:"name" json:"name"`

	// Query is a PromQL expression. It should return a single scalar value;
	// use aggregation functions (sum, max, avg) in the query itself.
	// Example: `sum(cnpg_backends_total{namespace="default", pod=~"pg-cluster-.*"})`
	Query string `yaml:"query" json:"query"`

	// ScaleUpThreshold: scale up when value exceeds this.
	ScaleUpThreshold float64 `yaml:"scaleUpThreshold" json:"scaleUpThreshold"`

	// ScaleDownThreshold: scale down when value falls below this.
	ScaleDownThreshold float64 `yaml:"scaleDownThreshold" json:"scaleDownThreshold"`

	// Weight controls this metric's influence when using weighted_average aggregation.
	// Range [0.0, 1.0]; defaults to 1.0 if 0.
	Weight float64 `yaml:"weight" json:"weight"`

	// TargetValuePerReplica: if > 0, desired replicas = ceil(value / TargetValuePerReplica).
	// If 0, falls back to threshold-based ±1 stepping.
	// Example: 50 connections per replica → set to 50.
	TargetValuePerReplica float64 `yaml:"targetValuePerReplica" json:"targetValuePerReplica"`
}

// Config is the full runtime configuration of the ScaleController.
// It can be swapped out live via ScaleController.UpdateConfig().
// Use LoadConfig to build it from a YAML file.
type Config struct {
	// MinInstances / MaxInstances are hard guardrails.
	MinInstances int
	MaxInstances int

	// Metrics is the list of Prometheus metrics to observe.
	Metrics []MetricSpec

	// Aggregation controls how signals from multiple metrics are combined.
	// AggregationMax (default): use the highest desired replica count across all metrics.
	// AggregationWeightedAverage: weighted average of desired replica counts.
	Aggregation AggregationType

	// Cooldown prevents scaling more than once per window to avoid flapping.
	Cooldown time.Duration

	// PollInterval controls how often the reconcile loop runs.
	PollInterval time.Duration

	// Prediction is optional predictive scaling configuration.
	// If nil or Enabled=false, prediction is skipped.
	Prediction *PredictionConfig
}

// AggregationType defines how per-metric desired replica counts are combined.
type AggregationType string

const (
	// AggregationMax takes the maximum desired replicas across all metrics.
	AggregationMax AggregationType = "max"

	// AggregationWeightedAverage takes the weighted average of desired replicas.
	AggregationWeightedAverage AggregationType = "weighted_average"
)

// PredictionConfig enables predictive scaling layered on top of reactive scaling.
type PredictionConfig struct {
	// Enabled toggles predictive scaling.
	Enabled bool

	// MetricName is the name of the metric (from Metrics list) to feed into the predictor.
	MetricName string

	// Horizon is how far ahead to forecast.
	// Example: 5m means "predict the metric value 5 minutes from now".
	Horizon time.Duration

	// MinHistoryDuration is the minimum age of the oldest data point required
	// before prediction is used. Prevents decisions on too little history.
	MinHistoryDuration time.Duration
}

// ScaleActionType describes the scaling action taken.
type ScaleActionType string

const (
	ScaleNone ScaleActionType = "none"
	ScaleUp   ScaleActionType = "scale_up"
	ScaleDown ScaleActionType = "scale_down"
)

// ScaleDecision is the output of ScaleController.Decide().
type ScaleDecision struct {
	Action          ScaleActionType
	TargetInstances int
	Reason          string

	// ReactiveTarget is the replica count computed from current metric values.
	ReactiveTarget int

	// PredictiveTarget is the replica count computed from forecasted values.
	// 0 means prediction was not used.
	PredictiveTarget int
}

// MetricsObserver fetches a snapshot of all configured metrics.
type MetricsObserver interface {
	Observe(ctx context.Context, specs []MetricSpec) (*MetricsSnapshot, error)
}
