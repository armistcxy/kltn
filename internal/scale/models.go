package scale

import (
	"context"
	"time"
)

// MetricsSnapshot holds a point-in-time snapshot of all configured metrics
type MetricsSnapshot struct {
	At     time.Time
	Values map[string]float64
}

// MetricSpec defines one metric to observe and how it drives scaling decisions
type MetricSpec struct {
	Name string `yaml:"name" json:"name"`

	Query                 string  `yaml:"query" json:"query"`
	ScaleUpThreshold      float64 `yaml:"scaleUpThreshold" json:"scaleUpThreshold"`
	ScaleDownThreshold    float64 `yaml:"scaleDownThreshold" json:"scaleDownThreshold"`
	Weight                float64 `yaml:"weight" json:"weight"`
	TargetValuePerReplica float64 `yaml:"targetValuePerReplica" json:"targetValuePerReplica"`
	ScaleDownGuard        bool    `yaml:"scaleDownGuard" json:"scaleDownGuard"`
	ScaleUpOnly           bool    `yaml:"scaleUpOnly" json:"scaleUpOnly"`
}

type Config struct {
	MinInstances int
	MaxInstances int

	Metrics []MetricSpec

	// Aggregation controls how signals from multiple metrics are combined
	// AggregationMax (default): use the highest desired replica count across all metrics.
	// AggregationWeightedAverage: weighted average of desired replica counts.
	Aggregation AggregationType

	// Cooldown prevents scaling more than once per window to avoid flapping
	Cooldown time.Duration

	// PollInterval controls how often the reconcile loop runs
	PollInterval time.Duration

	// Prediction is optional predictive scaling configuration
	Prediction *PredictionConfig

	// ScalingMode controls which signals drive the final replica count.
	// Valid values: "reactive", "predictive", "hybrid" (default).
	ScalingMode ScalingMode

	// ScaleDownStabilizationWindow is how long the reactive target must continuously stay below the current replica count
	// before a scale-down is allowed
	ScaleDownStabilizationWindow time.Duration
}

type ScalingMode string

const (
	// ScalingModeReactive uses only reactive (current-value) signals.
	ScalingModeReactive ScalingMode = "reactive"

	// ScalingModePredictive uses only predictive (forecast) signals.
	// Falls back to current replicas if prediction is not ready.
	ScalingModePredictive ScalingMode = "predictive"

	// ScalingModeHybrid takes max(reactive, predictive). This is the default.
	ScalingModeHybrid ScalingMode = "hybrid"
)

// AggregationType defines how per-metric desired replica counts are combined.
type AggregationType string

const (
	// AggregationMax takes the maximum desired replicas across all metrics.
	AggregationMax AggregationType = "max"

	// AggregationWeightedAverage takes the weighted average of desired replicas.
	AggregationWeightedAverage AggregationType = "weighted_average"
)

// PredictorType identifies the forecasting algorithm.
type PredictorType string

const (
	PredictorSMA         PredictorType = "sma"
	PredictorEWMA        PredictorType = "ewma"
	PredictorLinReg      PredictorType = "linreg"
	PredictorHoltWinters PredictorType = "holtwinters"
)

// SMAConfig configures the simple moving average predictor.
type SMAConfig struct {
	Window int `yaml:"window"`
}

type EWMAConfig struct {
	Alpha       float64 `yaml:"alpha"`
	TrendWindow int     `yaml:"trendWindow"`
}

type LinRegConfig struct {
	Window int `yaml:"window"`
}

type HoltWintersConfig struct {
	Alpha        float64 `yaml:"alpha"`
	Beta         float64 `yaml:"beta"`
	Gamma        float64 `yaml:"gamma"`
	SeasonLength int     `yaml:"seasonLength"`
}

// PredictionConfig enables predictive scaling layered on top of reactive scaling.
type PredictionConfig struct {
	Enabled    bool
	Type       PredictorType
	MetricName string

	Horizon            time.Duration
	MinHistoryDuration time.Duration

	SMA         *SMAConfig
	EWMA        *EWMAConfig
	LinReg      *LinRegConfig
	HoltWinters *HoltWintersConfig
}

type ScaleActionType string

const (
	ScaleNone ScaleActionType = "none"
	ScaleUp   ScaleActionType = "scale_up"
	ScaleDown ScaleActionType = "scale_down"
)

type ScaleDecision struct {
	Action           ScaleActionType
	TargetInstances  int
	Reason           string
	ReactiveTarget   int
	PredictiveTarget int
}

type MetricsObserver interface {
	Observe(ctx context.Context, specs []MetricSpec) (*MetricsSnapshot, error)
}
