package scale

import (
	"context"
	"fmt"
	"log"
	"time"
)

// ScaleController performs scaling actions based on observed metrics.
type ScaleController struct {
	cfg Config

	observer   MetricsObserver
	cnpgClient *CNPGClient

	lastScaleAt time.Time
}

func NewScaleController(
	cfg Config,
	observer MetricsObserver,
	cnpgClient *CNPGClient,
) *ScaleController {
	return &ScaleController{
		cfg:        cfg,
		observer:   observer,
		cnpgClient: cnpgClient,
	}
}

// Run is the main loop: Observe -> Decide -> Act
func (c *ScaleController) Run(ctx context.Context) error {
	log.Printf("Starting Scale Controller, checking metrics every %v", c.cfg.PollInterval)

	ticker := time.NewTicker(c.cfg.PollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if err := c.reconcileOnce(ctx); err != nil {
				return err
			}
		}
	}
}

// reconcileOnce executes one full loop.
func (c *ScaleController) reconcileOnce(ctx context.Context) error {
	log.Printf("Reconciling ...")

	// Stage 1: Observe metrics
	snapshot, err := c.observer.Observe(ctx)
	if err != nil {
		return fmt.Errorf("observe failed: %w", err)
	}
	log.Printf("Observed metrics at %v: totalBackends=%.0f, maxCPU=%.3f, maxMemory=%.3f, totalTPS=%.0f", snapshot.At, snapshot.TotalBackends, snapshot.MaxCPU, snapshot.MaxMemory, snapshot.TotalTPS)

	// Stage 2: Decide what to do based on metrics
	decision, err := c.Decide(ctx, snapshot)
	if err != nil {
		return fmt.Errorf("decide failed: %w", err)
	}
	log.Printf("Decide to %s to %d instances: %s",
		decision.Action, decision.TargetInstances, decision.Reason)

	// Stage 3: Act on decision
	if err := c.Act(ctx, decision); err != nil {
		return fmt.Errorf("act failed: %w", err)
	}
	log.Printf("Action %s applied successfully", decision.Action)
	log.Printf("Cluster now has %d instances", decision.TargetInstances)

	return nil
}

// Decide applies your scaling rules.
func (c *ScaleController) Decide(ctx context.Context, snapshot *MetricsSnapshot) (*ScaleDecision, error) {
	current, err := c.cnpgClient.GetCurrentInstances(ctx)
	if err != nil {
		return nil, err
	}

	// Cooldown check, prevent scaling too often
	if !c.lastScaleAt.IsZero() && time.Since(c.lastScaleAt) < c.cfg.Cooldown {
		return &ScaleDecision{
			Action:          ScaleNone,
			TargetInstances: current,
			Reason:          "cooldown active",
		}, nil
	}

	needScaleUp := snapshot.MaxCPU >= c.cfg.CPUScaleUpThreshold ||
		snapshot.MaxMemory >= c.cfg.MemoryScaleUpThreshold ||
		snapshot.TotalBackends >= c.cfg.BackendsScaleUpThreshold ||
		snapshot.TotalTPS >= c.cfg.TPSScaleUpThreshold

	needScaleDown := snapshot.MaxCPU <= c.cfg.CPUScaleDownThreshold &&
		snapshot.MaxMemory <= c.cfg.MemoryScaleDownThreshold &&
		snapshot.TotalBackends <= c.cfg.BackendsScaleDownThreshold &&
		snapshot.TotalTPS <= c.cfg.TPSScaleDownThreshold

	// Decide scale up
	if needScaleUp && current < c.cfg.MaxInstances {
		target := current + 1
		if target > c.cfg.MaxInstances {
			target = c.cfg.MaxInstances
		}
		return &ScaleDecision{
			Action:          ScaleUp,
			TargetInstances: target,
			Reason: fmt.Sprintf(
				"scale up: maxCPU=%.3f >= %.3f OR maxMemory=%.3f >= %.3f OR backends=%.0f >= %.0f OR tps=%.0f >= %.0f",
				snapshot.MaxCPU, c.cfg.CPUScaleUpThreshold,
				snapshot.MaxMemory, c.cfg.MemoryScaleUpThreshold,
				snapshot.TotalBackends, c.cfg.BackendsScaleUpThreshold,
				snapshot.TotalTPS, c.cfg.TPSScaleUpThreshold,
			),
		}, nil
	}

	// Decide scale down
	if needScaleDown && current > c.cfg.MinInstances {
		target := current - 1
		if target < c.cfg.MinInstances {
			target = c.cfg.MinInstances
		}
		return &ScaleDecision{
			Action:          ScaleDown,
			TargetInstances: target,
			Reason: fmt.Sprintf(
				"scale down: maxCPU=%.3f <= %.3f AND maxMemory=%.3f <= %.3f AND backends=%.0f <= %.0f AND tps=%.0f <= %.0f",
				snapshot.MaxCPU, c.cfg.CPUScaleDownThreshold,
				snapshot.MaxMemory, c.cfg.MemoryScaleDownThreshold,
				snapshot.TotalBackends, c.cfg.BackendsScaleDownThreshold,
				snapshot.TotalTPS, c.cfg.TPSScaleDownThreshold,
			),
		}, nil
	}

	return &ScaleDecision{
		Action:          ScaleNone,
		TargetInstances: current,
		Reason:          "no scaling needed",
	}, nil
}

// Act applies the scaling decision by patching CNPG Cluster.
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
