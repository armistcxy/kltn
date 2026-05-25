package scale

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"sync"
	"time"
)

// ScaleController is the main control loop: Observe -> Decide -> Act.
type ScaleController struct {
	mu  sync.RWMutex
	cfg Config

	observer   MetricsObserver
	cnpgClient *CNPGClient
	predictor  Predictor
	metrics    *ControllerMetrics

	historyMu sync.Mutex
	history   map[string][]DataPoint

	stabilizationMu      sync.Mutex
	reactiveTargetWindow []reactiveRecord

	lastScaleAt time.Time
}

type reactiveRecord struct {
	at     time.Time
	target int
}

func NewScaleController(cfg Config, observer MetricsObserver, cnpgClient *CNPGClient) *ScaleController {
	return &ScaleController{
		cfg:        cfg,
		observer:   observer,
		cnpgClient: cnpgClient,
		history:    make(map[string][]DataPoint),
	}
}

func (c *ScaleController) WithMetrics(m *ControllerMetrics) *ScaleController {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.metrics = m
	return c
}

func (c *ScaleController) WithPredictor(p Predictor) *ScaleController {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.predictor = p
	slog.Info("predictor attached", "algorithm", p.Name())
	return c
}

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
		"scalingMode", cfg.ScalingMode,
	)
	c.cfg = cfg
}

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
			if newCfg := c.getConfig(); newCfg.PollInterval != cfg.PollInterval {
				cfg = newCfg
				ticker.Reset(cfg.PollInterval)
				slog.Info("poll interval updated", "pollInterval", cfg.PollInterval)
			}

			if err := c.reconcileOnce(ctx); err != nil {
				slog.Error("reconcile error", "err", err)
			}
		}
	}
}

func (c *ScaleController) reconcileOnce(ctx context.Context) error {
	cfg := c.getConfig()

	// Observe
	snapshot, err := c.observer.Observe(ctx, cfg.Metrics)
	if err != nil {
		return fmt.Errorf("observe: %w", err)
	}
	slog.Info("metrics observed", "at", snapshot.At, "values", snapshot.Values)

	if c.metrics != nil {
		for name, value := range snapshot.Values {
			c.metrics.recordRaw(name, value)
		}
	}

	c.appendHistory(snapshot)

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
		"mode", cfg.ScalingMode,
		"reason", decision.Reason,
	)

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

func (c *ScaleController) Decide(ctx context.Context, cfg Config, snapshot *MetricsSnapshot) (*ScaleDecision, error) {
	current, err := c.cnpgClient.GetCurrentInstances(ctx)
	if err != nil {
		return nil, fmt.Errorf("get current instances: %w", err)
	}

	// Cooldown guard
	if !c.lastScaleAt.IsZero() && time.Since(c.lastScaleAt) < cfg.Cooldown {
		return &ScaleDecision{
			Action:          ScaleNone,
			TargetInstances: current,
			Reason:          fmt.Sprintf("cooldown active (next scale in %v)", cfg.Cooldown-time.Since(c.lastScaleAt).Round(time.Second)),
		}, nil
	}

	rawReactive, triggerMetric := c.computeReactiveTarget(cfg, snapshot, current)
	reactiveTarget := c.stabilizedReactiveTarget(rawReactive, cfg.ScaleDownStabilizationWindow)
	if reactiveTarget != rawReactive {
		slog.Info("scale-down stabilization active",
			"rawReactive", rawReactive,
			"stabilizedReactive", reactiveTarget,
			"window", cfg.ScaleDownStabilizationWindow,
		)
	}

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

	var target int
	switch cfg.ScalingMode {
	case ScalingModeReactive:
		target = reactiveTarget
	case ScalingModePredictive:
		if predictiveTarget == -1 {
			slog.Info("predictive mode: waiting for sufficient history")
			target = current
		} else {
			target = predictiveTarget
		}
	default: // ScalingModeHybrid
		if predictiveTarget == -1 {
			target = reactiveTarget
		} else {
			target = max(reactiveTarget, predictiveTarget)
		}
	}
	target = clamp(target, cfg.MinInstances, cfg.MaxInstances)

	// Scale-down guard (block scale-down if any guard metric is still above its scaleDownThreshold)
	if target < current {
		for _, spec := range cfg.Metrics {
			if !spec.ScaleDownGuard {
				continue
			}
			v, ok := snapshot.Values[spec.Name]
			if ok && v > spec.ScaleDownThreshold {
				slog.Warn("scale-down blocked by guard metric",
					"metric", spec.Name,
					"value", v,
					"scaleDownThreshold", spec.ScaleDownThreshold,
				)
				target = current
				break
			}
		}
	}

	decision := &ScaleDecision{
		TargetInstances:  target,
		ReactiveTarget:   reactiveTarget,
		PredictiveTarget: predictiveTarget,
	}
	switch {
	case target > current:
		decision.Action = ScaleUp
		decision.Reason = fmt.Sprintf(
			"scale up %d -> %d (trigger=%s, reactive=%d, predictive=%d)",
			current, target, triggerMetric, reactiveTarget, predictiveTarget,
		)
	case target < current:
		decision.Action = ScaleDown
		decision.Reason = fmt.Sprintf(
			"scale down %d -> %d (trigger=%s, reactive=%d, predictive=%d)",
			current, target, triggerMetric, reactiveTarget, predictiveTarget,
		)
	default:
		decision.Action = ScaleNone
		decision.Reason = "no scaling needed"
	}

	return decision, nil
}

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

func (c *ScaleController) stabilizedReactiveTarget(reactiveTarget int, window time.Duration) int {
	if window <= 0 {
		return reactiveTarget
	}

	now := time.Now()
	cutoff := now.Add(-window)

	c.stabilizationMu.Lock()
	defer c.stabilizationMu.Unlock()

	c.reactiveTargetWindow = append(c.reactiveTargetWindow, reactiveRecord{at: now, target: reactiveTarget})

	i := 0
	for i < len(c.reactiveTargetWindow) && c.reactiveTargetWindow[i].at.Before(cutoff) {
		i++
	}
	c.reactiveTargetWindow = c.reactiveTargetWindow[i:]

	maxTarget := reactiveTarget
	for _, r := range c.reactiveTargetWindow {
		if r.target > maxTarget {
			maxTarget = r.target
		}
	}
	return maxTarget
}

func (c *ScaleController) computeReactiveTarget(cfg Config, snapshot *MetricsSnapshot, current int) (int, string) {
	if len(cfg.Metrics) == 0 {
		return current, ""
	}

	type metricDesire struct {
		name    string
		value   float64
		desired int
		reason  string
	}

	desires := make([]float64, 0, len(cfg.Metrics))
	weights := make([]float64, 0, len(cfg.Metrics))
	entries := make([]metricDesire, 0, len(cfg.Metrics))

	for _, spec := range cfg.Metrics {
		value, ok := snapshot.Values[spec.Name]
		if !ok {
			slog.Warn("metric missing from snapshot", "metric", spec.Name)
			continue
		}

		desired, reason := desiredReplicasForMetric(spec, value, current)

		if spec.ScaleUpOnly && desired < current {
			slog.Info("scaleUpOnly metric skipped (scale-down suppressed)", "metric", spec.Name, "value", value, "desired", desired)
			continue
		}

		w := spec.Weight
		if w <= 0 {
			w = 1.0
		}
		desires = append(desires, float64(desired))
		weights = append(weights, w)
		entries = append(entries, metricDesire{name: spec.Name, value: value, desired: desired, reason: reason})
		slog.Info("metric evaluated",
			"metric", spec.Name,
			"value", value,
			"desiredReplicas", desired,
			"reason", reason,
		)
	}

	if len(desires) == 0 {
		return current, ""
	}

	var target int
	switch cfg.Aggregation {
	case AggregationWeightedAverage:
		target = int(math.Ceil(weightedAverage(desires, weights)))
	default:
		target = int(maxSlice(desires))
	}

	trigger := ""
	for _, e := range entries {
		if e.desired == target {
			trigger = e.name
			break
		}
	}
	return target, trigger
}

func desiredReplicasForMetric(spec MetricSpec, value float64, current int) (int, string) {
	var desired int
	var reason string

	if spec.TargetValuePerReplica > 0 {
		desired = int(math.Ceil(value / spec.TargetValuePerReplica))
		reason = fmt.Sprintf("ratio: %.2f / %.2f per replica = %d", value, spec.TargetValuePerReplica, desired)
	} else if value >= spec.ScaleUpThreshold {
		desired = current + 1
		reason = fmt.Sprintf("value %.4f >= scaleUpThreshold %.4f", value, spec.ScaleUpThreshold)
	} else if value <= spec.ScaleDownThreshold {
		desired = current - 1
		reason = fmt.Sprintf("value %.4f <= scaleDownThreshold %.4f", value, spec.ScaleDownThreshold)
	} else {
		desired = current
		reason = fmt.Sprintf("value %.4f within thresholds [%.4f, %.4f]", value, spec.ScaleDownThreshold, spec.ScaleUpThreshold)
	}

	return desired, reason
}

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
		return -1, nil
	}

	if pred.MinHistoryDuration > 0 {
		age := time.Since(history[0].Timestamp)
		if age < pred.MinHistoryDuration {
			slog.Info("insufficient history for prediction",
				"metric", pred.MetricName,
				"have", age.Round(time.Second),
				"need", pred.MinHistoryDuration,
			)
			return -1, nil
		}
	}

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

	for _, spec := range cfg.Metrics {
		if spec.Name == pred.MetricName {
			desired, _ := desiredReplicasForMetric(spec, predicted, current)
			return desired, nil
		}
	}

	return current, fmt.Errorf("prediction metric %q not found in metrics list", pred.MetricName)
}

const maxHistorySize = 2880

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
