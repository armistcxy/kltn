package scale

import (
	"context"
	"testing"
	"time"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrlclientfake "sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func newFakeCNPGClientWithInstances(instances int) *CNPGClient {
	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(cnpgv1.AddToScheme(scheme))

	cluster := &cnpgv1.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pg-cluster",
			Namespace: "default",
		},
		Spec: cnpgv1.ClusterSpec{
			Instances: instances,
		},
	}

	fakeClient := ctrlclientfake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(cluster).
		Build()

	return NewCNPGClient(fakeClient, "default", "pg-cluster")
}

// scalingModeCfg returns a minimal Config for scaling-mode decision tests.
// With TargetValuePerReplica=50:
//   - snapshot value=100  → reactive  = ceil(100/50) = 2
//   - history avg=150     → predicted ≈ 150 → predictive = ceil(150/50) = 3
func scalingModeCfg(mode ScalingMode) Config {
	return Config{
		MinInstances: 1,
		MaxInstances: 6,
		PollInterval: 30 * time.Second,
		Cooldown:     0,
		ScalingMode:  mode,
		Metrics: []MetricSpec{
			{
				Name:                  "backends",
				Query:                 "sum(cnpg_backends_total)",
				ScaleUpThreshold:      80,
				ScaleDownThreshold:    20,
				TargetValuePerReplica: 50,
				Weight:                1.0,
			},
		},
		Prediction: &PredictionConfig{
			Enabled:    true,
			Type:       PredictorSMA,
			MetricName: "backends",
			Horizon:    5 * time.Minute,
			SMA:        &SMAConfig{Window: 10},
		},
	}
}

// newTestControllerWithHistory builds a ScaleController with 1h of history (value=150)
// seeded so the MovingAveragePredictor returns ≈150 → predictive target = 3.
func newTestControllerWithHistory(t *testing.T, mode ScalingMode, current int) *ScaleController {
	t.Helper()
	cfg := scalingModeCfg(mode)
	cnpg := newFakeCNPGClientWithInstances(current)
	c := NewScaleController(cfg, &noopObserver{}, cnpg)
	c.WithPredictor(NewMovingAveragePredictor(10))

	// Seed 120 points × 30 s = 1 h of history with value=150.
	now := time.Now()
	c.historyMu.Lock()
	pts := make([]DataPoint, 120)
	for i := range pts {
		pts[i] = DataPoint{
			Timestamp: now.Add(-time.Duration(120-i) * 30 * time.Second),
			Value:     150,
		}
	}
	c.history["backends"] = pts
	c.historyMu.Unlock()

	return c
}

// noopObserver is a no-op MetricsObserver used in Decide() unit tests.
type noopObserver struct{}

func (n *noopObserver) Observe(_ context.Context, _ []MetricSpec) (*MetricsSnapshot, error) {
	return &MetricsSnapshot{At: time.Now(), Values: map[string]float64{}}, nil
}

func backendsSnapshot(value float64) *MetricsSnapshot {
	return &MetricsSnapshot{At: time.Now(), Values: map[string]float64{"backends": value}}
}

// ---------------------------------------------------------------------------
// Scaling mode decision tests
// ---------------------------------------------------------------------------

// reactive=2, predictive=3, mode=reactive → final=2
func TestScalingMode_Reactive(t *testing.T) {
	c := newTestControllerWithHistory(t, ScalingModeReactive, 1)
	decision, err := c.Decide(context.Background(), c.cfg, backendsSnapshot(100))
	if err != nil {
		t.Fatal(err)
	}
	if decision.ReactiveTarget != 2 {
		t.Errorf("reactive target: want 2, got %d", decision.ReactiveTarget)
	}
	if decision.TargetInstances != 2 {
		t.Errorf("final target (reactive mode): want 2, got %d", decision.TargetInstances)
	}
}

// reactive=2, predictive=3, mode=predictive → final=3
func TestScalingMode_Predictive(t *testing.T) {
	c := newTestControllerWithHistory(t, ScalingModePredictive, 1)
	decision, err := c.Decide(context.Background(), c.cfg, backendsSnapshot(100))
	if err != nil {
		t.Fatal(err)
	}
	if decision.PredictiveTarget != 3 {
		t.Errorf("predictive target: want 3, got %d", decision.PredictiveTarget)
	}
	if decision.TargetInstances != 3 {
		t.Errorf("final target (predictive mode): want 3, got %d", decision.TargetInstances)
	}
}

// reactive=2, predictive=3, mode=hybrid → final=3
func TestScalingMode_Hybrid(t *testing.T) {
	c := newTestControllerWithHistory(t, ScalingModeHybrid, 1)
	decision, err := c.Decide(context.Background(), c.cfg, backendsSnapshot(100))
	if err != nil {
		t.Fatal(err)
	}
	if decision.TargetInstances != 3 {
		t.Errorf("final target (hybrid mode): want 3, got %d", decision.TargetInstances)
	}
}

// mode=predictive + no history → final=currentReplicas (hold)
func TestScalingMode_Predictive_NoHistory(t *testing.T) {
	current := 2
	cfg := scalingModeCfg(ScalingModePredictive)
	cnpg := newFakeCNPGClientWithInstances(current)
	c := NewScaleController(cfg, &noopObserver{}, cnpg)
	c.WithPredictor(NewMovingAveragePredictor(10))
	// No history → computePredictiveTarget returns current, predictiveTarget stays 0.

	decision, err := c.Decide(context.Background(), c.cfg, backendsSnapshot(100))
	if err != nil {
		t.Fatal(err)
	}
	if decision.TargetInstances != current {
		t.Errorf("predictive mode (no history): want %d (hold), got %d", current, decision.TargetInstances)
	}
}

// ---------------------------------------------------------------------------
// validateConfig: scalingMode validation
// ---------------------------------------------------------------------------

func TestValidateConfig_ScalingMode(t *testing.T) {
	base := Config{
		MinInstances: 1,
		MaxInstances: 3,
		PollInterval: 30 * time.Second,
		Cooldown:     60 * time.Second,
		ScalingMode:  ScalingModeHybrid,
		Metrics:      []MetricSpec{{Name: "m", Query: "q"}},
	}

	for _, mode := range []ScalingMode{ScalingModeReactive, ScalingModePredictive, ScalingModeHybrid} {
		cfg := base
		cfg.ScalingMode = mode
		if err := validateConfig(cfg); err != nil {
			t.Errorf("mode=%q: unexpected error: %v", mode, err)
		}
	}

	cfg := base
	cfg.ScalingMode = "invalid"
	if err := validateConfig(cfg); err == nil {
		t.Error("expected error for invalid scalingMode, got nil")
	}
}

// ---------------------------------------------------------------------------
// Scale-down stabilization window tests
// ---------------------------------------------------------------------------

// stabilizationCfg builds a reactive-only config with a stabilization window.
// With targetValuePerReplica=10 and current=6:
//   - snapshot value=40  → rawReactive = ceil(40/10) = 4  (below current → wants scale-down)
//   - snapshot value=76  → rawReactive = ceil(76/10) = 8  (above current → wants scale-up)
func stabilizationCfg(window time.Duration) Config {
	return Config{
		MinInstances:                 1,
		MaxInstances:                 10,
		PollInterval:                 30 * time.Second,
		Cooldown:                     0,
		ScalingMode:                  ScalingModeReactive,
		ScaleDownStabilizationWindow: window,
		Metrics: []MetricSpec{
			{
				Name:                  "backends",
				Query:                 "q",
				ScaleUpThreshold:      80,
				ScaleDownThreshold:    20,
				TargetValuePerReplica: 10,
				Weight:                1.0,
			},
		},
	}
}

// TestStabilization_BlocksTransientDip simulates the 76→4→71 pattern.
// Even though the middle poll returns rawReactive=1 (below current=6),
// the stabilization window keeps the floor at 8 (from the high poll),
// so no scale-down is issued.
func TestStabilization_BlocksTransientDip(t *testing.T) {
	current := 6
	cfg := stabilizationCfg(2 * time.Minute)
	c := NewScaleController(cfg, &noopObserver{}, newFakeCNPGClientWithInstances(current))

	// Poll 1: backends=76 → rawReactive=8, window max=8
	d, err := c.Decide(context.Background(), cfg, backendsSnapshot(76))
	if err != nil {
		t.Fatal(err)
	}
	if d.Action != ScaleUp {
		t.Errorf("poll1: want scale_up, got %s", d.Action)
	}

	// Poll 2: backends=4 → rawReactive=1, but window max is still 8 → no scale-down
	d, err = c.Decide(context.Background(), cfg, backendsSnapshot(4))
	if err != nil {
		t.Fatal(err)
	}
	if d.Action == ScaleDown {
		t.Errorf("poll2 (transient dip): scale-down should be blocked by stabilization window, got %s", d.Action)
	}
}

// TestStabilization_AllowsScaleDownAfterWindow verifies that once the stabilization
// window expires (all entries are low), scale-down is permitted.
func TestStabilization_AllowsScaleDownAfterWindow(t *testing.T) {
	current := 6
	// Zero window → stabilization disabled, scale-down allowed immediately.
	cfg := stabilizationCfg(0)
	c := NewScaleController(cfg, &noopObserver{}, newFakeCNPGClientWithInstances(current))

	d, err := c.Decide(context.Background(), cfg, backendsSnapshot(4))
	if err != nil {
		t.Fatal(err)
	}
	if d.Action != ScaleDown {
		t.Errorf("window=0: want scale_down, got %s (target=%d)", d.Action, d.TargetInstances)
	}
}

// TestStabilizedReactiveTarget_MaxOverWindow unit-tests the helper directly.
func TestStabilizedReactiveTarget_MaxOverWindow(t *testing.T) {
	c := &ScaleController{}

	window := 5 * time.Minute

	// Feed a high value then a low value — max should remain high.
	got := c.stabilizedReactiveTarget(8, window)
	if got != 8 {
		t.Errorf("after high value: want 8, got %d", got)
	}
	got = c.stabilizedReactiveTarget(1, window)
	if got != 8 {
		t.Errorf("after low value (window not expired): want 8, got %d", got)
	}

	// Manually back-date all records so they fall outside the window.
	c.stabilizationMu.Lock()
	for i := range c.reactiveTargetWindow {
		c.reactiveTargetWindow[i].at = time.Now().Add(-10 * time.Minute)
	}
	c.stabilizationMu.Unlock()

	// Next call should evict all old records; only the new value (2) remains.
	got = c.stabilizedReactiveTarget(2, window)
	if got != 2 {
		t.Errorf("after window expiry: want 2, got %d", got)
	}
}
