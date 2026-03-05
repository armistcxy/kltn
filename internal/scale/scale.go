package scale

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"sync"
	"time"
)

// ScaleController is the main control loop: Observe → Decide → Act.
//
// Its configuration is fully dynamic: call UpdateConfig() at any time
// to change metrics, thresholds, aggregation, or prediction settings
// without restarting the controller.
//
// Predictive scaling is optional and pluggable: attach any Predictor
// implementation via WithPredictor(). If no predictor is set, or if
// prediction is disabled in Config, only reactive scaling is used.
type ScaleController struct {
	mu  sync.RWMutex
	cfg Config

	observer   MetricsObserver
	cnpgClient *CNPGClient
	predictor  Predictor // nil → reactive only
	metrics    *ControllerMetrics // nil → no metrics exported

	// Rolling per-metric history, used by the predictor.
	historyMu sync.Mutex
	history   map[string][]DataPoint

	lastScaleAt time.Time
}

// NewScaleController creates a ScaleController with the given initial config.
func NewScaleController(cfg Config, observer MetricsObserver, cnpgClient *CNPGClient) *ScaleController {
	return &ScaleController{
		cfg:        cfg,
		observer:   observer,
		cnpgClient: cnpgClient,
		history:    make(map[string][]DataPoint),
	}
}

// WithMetrics attaches a ControllerMetrics instance that the controller will use
// to export Prometheus metrics on every reconcile cycle.
// Can be called before Run() or concurrently; it is goroutine-safe.
func (c *ScaleController) WithMetrics(m *ControllerMetrics) *ScaleController {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.metrics = m
	return c
}

// WithPredictor attaches a predictive scaling algorithm.
// Can be called before Run() or concurrently; it is goroutine-safe.
func (c *ScaleController) WithPredictor(p Predictor) *ScaleController {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.predictor = p
	slog.Info("predictor attached", "algorithm", p.Name())
	return c
}

// UpdateConfig replaces the running configuration atomically.
// Safe to call from any goroutine while Run() is active.
// The new config takes effect on the next reconcile iteration.
func (c *ScaleController) UpdateConfig(cfg Config) {
	c.mu.Lock()
	defer c.mu.Unlock()
	slog.Info("config updated",
		"metrics", len(cfg.Metrics),
		"min", cfg.MinInstances,
		"max", cfg.MaxInstances,
		"pollInterval", cfg.PollInterval,
		"cooldown", cfg.Cooldown,
		"aggregation", cfg.Aggregation,
	)
	c.cfg = cfg
}

// Run starts the reconcile loop and blocks until ctx is cancelled.
func (c *ScaleController) Run(ctx context.Context) error {
	cfg := c.getConfig()
	slog.Info("scale controller started",
		"pollInterval", cfg.PollInterval,
		"metrics", len(cfg.Metrics),
	)

	ticker := time.NewTicker(cfg.PollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			// Refresh ticker if pollInterval changed at runtime.
			if newCfg := c.getConfig(); newCfg.PollInterval != cfg.PollInterval {
				cfg = newCfg
				ticker.Reset(cfg.PollInterval)
				slog.Info("poll interval updated", "pollInterval", cfg.PollInterval)
			}

			if err := c.reconcileOnce(ctx); err != nil {
				// Log and continue: a transient error should not crash the loop.
				slog.Error("reconcile error", "err", err)
			}
		}
	}
}

// reconcileOnce executes one full Observe → Decide → Act cycle.
func (c *ScaleController) reconcileOnce(ctx context.Context) error {
	cfg := c.getConfig()

	// Observe
	snapshot, err := c.observer.Observe(ctx, cfg.Metrics)
	if err != nil {
		return fmt.Errorf("observe: %w", err)
	}
	slog.Info("metrics observed", "at", snapshot.At, "values", snapshot.Values)

	// Export raw (instantaneous) metric values.
	if c.metrics != nil {
		for name, value := range snapshot.Values {
			c.metrics.recordRaw(name, value)
		}
	}

	// Record into rolling history for the predictor.
	c.appendHistory(snapshot)

	// Export moving-average values (computed over the full history buffer).
	if c.metrics != nil {
		for name := range snapshot.Values {
			c.metrics.recordAvg(name, c.computeHistoryAvg(name))
		}
	}

	// Decide
	decision, err := c.Decide(ctx, cfg, snapshot)
	if err != nil {
		return fmt.Errorf("decide: %w", err)
	}
	slog.Info("decision",
		"action", decision.Action,
		"target", decision.TargetInstances,
		"reactive", decision.ReactiveTarget,
		"predictive", decision.PredictiveTarget,
		"reason", decision.Reason,
	)

	// Export decision metrics.
	if c.metrics != nil {
		current, _ := c.cnpgClient.GetCurrentInstances(ctx)
		c.metrics.recordDecision(current, decision.ReactiveTarget, decision.PredictiveTarget, decision.TargetInstances)
	}

	// Act
	if err := c.Act(ctx, decision); err != nil {
		return fmt.Errorf("act: %w", err)
	}

	return nil
}

// Decide computes the scaling decision from a metrics snapshot.
// It is exported so callers can unit-test decision logic directly.
func (c *ScaleController) Decide(ctx context.Context, cfg Config, snapshot *MetricsSnapshot) (*ScaleDecision, error) {
	current, err := c.cnpgClient.GetCurrentInstances(ctx)
	if err != nil {
		return nil, fmt.Errorf("get current instances: %w", err)
	}

	// Cooldown guard.
	if !c.lastScaleAt.IsZero() && time.Since(c.lastScaleAt) < cfg.Cooldown {
		return &ScaleDecision{
			Action:          ScaleNone,
			TargetInstances: current,
			Reason:          fmt.Sprintf("cooldown active (next scale in %v)", cfg.Cooldown-time.Since(c.lastScaleAt).Round(time.Second)),
		}, nil
	}

	// Reactive target: compute desired replicas from current metric values.
	reactiveTarget := c.computeReactiveTarget(cfg, snapshot, current)

	// Predictive target: use the predictor to forecast the primary metric and map to desired replicas.
	predictiveTarget := 0
	predictor := c.getPredictor()
	if cfg.Prediction != nil && cfg.Prediction.Enabled && predictor != nil {
		pt, err := c.computePredictiveTarget(ctx, cfg, current, predictor)
		if err != nil {
			slog.Warn("predictive scaling skipped", "err", err)
		} else {
			predictiveTarget = pt
		}
	}

	// Conservative approach, take the maximum of reactive and predictive.
	target := reactiveTarget
	if predictiveTarget > target {
		target = predictiveTarget
	}
	target = clamp(target, cfg.MinInstances, cfg.MaxInstances)

	decision := &ScaleDecision{
		TargetInstances:  target,
		ReactiveTarget:   reactiveTarget,
		PredictiveTarget: predictiveTarget,
	}
	switch {
	case target > current:
		decision.Action = ScaleUp
		decision.Reason = fmt.Sprintf(
			"scale up %d → %d (reactive=%d, predictive=%d)",
			current, target, reactiveTarget, predictiveTarget,
		)
	case target < current:
		decision.Action = ScaleDown
		decision.Reason = fmt.Sprintf(
			"scale down %d → %d (reactive=%d, predictive=%d)",
			current, target, reactiveTarget, predictiveTarget,
		)
	default:
		decision.Action = ScaleNone
		decision.Reason = "no scaling needed"
	}

	return decision, nil
}

// Act applies the scaling decision by patching the CNPG Cluster.
func (c *ScaleController) Act(ctx context.Context, d *ScaleDecision) error {
	if d.Action == ScaleNone {
		return nil
	}
	if err := c.cnpgClient.PatchInstances(ctx, d.TargetInstances); err != nil {
		return err
	}
	c.lastScaleAt = time.Now()
	return nil
}

// computeReactiveTarget calculates desired replicas from the current snapshot.
// Each metric independently produces a desired count; the aggregation strategy
// (max or weighted_average) combines them into a single number.
func (c *ScaleController) computeReactiveTarget(cfg Config, snapshot *MetricsSnapshot, current int) int {
	if len(cfg.Metrics) == 0 {
		return current
	}

	desires := make([]float64, 0, len(cfg.Metrics))
	weights := make([]float64, 0, len(cfg.Metrics))

	for _, spec := range cfg.Metrics {
		value, ok := snapshot.Values[spec.Name]
		if !ok {
			slog.Warn("metric missing from snapshot", "metric", spec.Name)
			continue
		}

		desired := desiredReplicasForMetric(spec, value, current)
		w := spec.Weight
		if w <= 0 {
			w = 1.0
		}
		desires = append(desires, float64(desired))
		weights = append(weights, w)
	}

	if len(desires) == 0 {
		return current
	}

	switch cfg.Aggregation {
	case AggregationWeightedAverage:
		return int(math.Ceil(weightedAverage(desires, weights)))
	default: // AggregationMax
		return int(maxSlice(desires))
	}
}

// desiredReplicasForMetric maps a single metric value to a desired replica count.
//
// If TargetValuePerReplica > 0:  desired = ceil(value / TargetValuePerReplica)
// Otherwise: threshold-based ±1 step from current.
func desiredReplicasForMetric(spec MetricSpec, value float64, current int) int {
	if spec.TargetValuePerReplica > 0 {
		return int(math.Ceil(value / spec.TargetValuePerReplica))
	}
	if value >= spec.ScaleUpThreshold {
		return current + 1
	}
	if value <= spec.ScaleDownThreshold {
		return current - 1
	}
	return current
}

// computePredictiveTarget uses the injected Predictor to forecast the primary
// metric and returns the desired replica count based on that forecast.
// This is the integration point for any forecasting algorithm.
func (c *ScaleController) computePredictiveTarget(
	ctx context.Context,
	cfg Config,
	current int,
	predictor Predictor,
) (int, error) {
	pred := cfg.Prediction

	history := c.getHistory(pred.MetricName)
	if len(history) == 0 {
		slog.Info("no history yet for prediction", "metric", pred.MetricName)
		return current, nil
	}

	// Guard: require a minimum amount of history before trusting the forecast.
	if pred.MinHistoryDuration > 0 {
		age := time.Since(history[0].Timestamp)
		if age < pred.MinHistoryDuration {
			slog.Info("insufficient history for prediction",
				"metric", pred.MetricName,
				"have", age.Round(time.Second),
				"need", pred.MinHistoryDuration,
			)
			return current, nil
		}
	}

	// Call the injected predictor function to get the forecasted metric value at now+horizon.
	predicted, err := predictor.Predict(ctx, history, pred.Horizon)
	if err != nil {
		return current, fmt.Errorf("predictor %q: %w", predictor.Name(), err)
	}
	slog.Info("prediction result",
		"algorithm", predictor.Name(),
		"metric", pred.MetricName,
		"predicted", predicted,
		"horizon", pred.Horizon,
	)

	// Map the predicted value → desired replicas using the same spec as reactive.
	for _, spec := range cfg.Metrics {
		if spec.Name == pred.MetricName {
			return desiredReplicasForMetric(spec, predicted, current), nil
		}
	}

	return current, fmt.Errorf("prediction metric %q not found in metrics list", pred.MetricName)
}

// History management for the predictor. We keep a rolling history of observed metric values,

const maxHistorySize = 2880 // 24 h at 30 s intervals

// appendHistory records new metric values into the rolling per-metric history.
func (c *ScaleController) appendHistory(snapshot *MetricsSnapshot) {
	c.historyMu.Lock()
	defer c.historyMu.Unlock()

	for name, value := range snapshot.Values {
		c.history[name] = append(c.history[name], DataPoint{
			Timestamp: snapshot.At,
			Value:     value,
		})
		if len(c.history[name]) > maxHistorySize {
			c.history[name] = c.history[name][len(c.history[name])-maxHistorySize:]
		}
	}
}

// computeHistoryAvg returns the arithmetic mean of all values in the rolling
// history for metricName. Returns 0 if no history exists yet.
func (c *ScaleController) computeHistoryAvg(metricName string) float64 {
	c.historyMu.Lock()
	defer c.historyMu.Unlock()

	h := c.history[metricName]
	if len(h) == 0 {
		return 0
	}
	sum := 0.0
	for _, dp := range h {
		sum += dp.Value
	}
	return sum / float64(len(h))
}

// getHistory returns a copy of the history for the named metric (oldest-first).
func (c *ScaleController) getHistory(metricName string) []DataPoint {
	c.historyMu.Lock()
	defer c.historyMu.Unlock()

	h := c.history[metricName]
	if len(h) == 0 {
		return nil
	}
	out := make([]DataPoint, len(h))
	copy(out, h)
	return out
}

// Helper functions for decision logic.

func (c *ScaleController) getConfig() Config {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.cfg
}

func (c *ScaleController) getPredictor() Predictor {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.predictor
}

func clamp(v, min, max int) int {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}

func weightedAverage(values, weights []float64) float64 {
	totalWeight, sum := 0.0, 0.0
	for i, v := range values {
		sum += v * weights[i]
		totalWeight += weights[i]
	}
	if totalWeight == 0 {
		return 0
	}
	return sum / totalWeight
}

func maxSlice(values []float64) float64 {
	m := values[0]
	for _, v := range values[1:] {
		if v > m {
			m = v
		}
	}
	return m
}
