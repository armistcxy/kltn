package storage

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"time"
)

// Controller is the storage autoscaling control loop: Observe → Decide → Act → Confirm.
// It runs independently from the instance ScaleController.
type Controller struct {
	cfg      Config
	observer *Observer
	decider  *Decider
	actor    *Actor

	rootCtx context.Context // set in Run(); used by confirmation goroutines

	lastPGDataResizeAt time.Time
	lastWALResizeAt    time.Time
}

// NewController creates a storage Controller with all dependencies wired.
func NewController(cfg Config, observer *Observer, decider *Decider, actor *Actor) *Controller {
	return &Controller{
		cfg:      cfg,
		observer: observer,
		decider:  decider,
		actor:    actor,
	}
}

// Run starts the reconcile loop and blocks until ctx is cancelled.
func (c *Controller) Run(ctx context.Context) error {
	c.rootCtx = ctx
	slog.Info("storage controller started",
		"pollInterval", c.cfg.PollInterval,
		"namespace", c.cfg.Namespace,
		"cluster", c.cfg.Cluster,
	)

	ticker := time.NewTicker(c.cfg.PollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if err := c.reconcileOnce(ctx); err != nil {
				slog.Error("storage reconcile error", "err", err)
			}
		}
	}
}

// reconcileOnce executes one full Observe → Decide → Act cycle.
func (c *Controller) reconcileOnce(ctx context.Context) error {
	// Observe
	snap, err := c.observer.Observe(ctx, c.cfg)
	if err != nil {
		return fmt.Errorf("observe: %w", err)
	}
	slog.Info("storage metrics observed",
		"at", snap.At,
		"pgdata_usage_pct", snap.PGDataUsagePercent,
		"pgdata_time_to_full_h", snap.PGDataTimeToFullSeconds/3600,
		"pgdata_worst_case_growth_bytes_s", snap.PGDataWorstCaseGrowthRateBytesPerSec,
		"wal_usage_ratio", snap.WALUsageRatio,
		"wal_archive_pending", snap.WALArchivePending,
		"replication_lag_s", snap.ReplicationLagSeconds,
		"pgdata_size", snap.CurrentPGDataSize,
		"wal_size", snap.CurrentWALSize,
	)

	// Update state gauges.
	if !math.IsNaN(snap.PGDataUsagePercent) {
		pgdataUsagePercent.Set(snap.PGDataUsagePercent)
	}
	if !math.IsNaN(snap.PGDataTimeToFullSeconds) {
		pgdataTimeToFullHours.Set(snap.PGDataTimeToFullSeconds / 3600)
	}
	if !math.IsNaN(snap.WALUsageRatio) {
		walUsagePercent.Set(snap.WALUsageRatio * 100)
	}

	// Decide + Act: PGDATA
	pgDataDecision := c.decider.DecidePGData(snap, c.cfg.PGData, c.cfg.SafetyGuards, c.lastPGDataResizeAt)
	slog.Info("pgdata decision",
		"should_resize", pgDataDecision.ShouldResize,
		"old_size", pgDataDecision.OldSize,
		"new_size", pgDataDecision.NewSize,
		"reason", pgDataDecision.Reason,
	)
	if strings.HasPrefix(pgDataDecision.Reason, "blocked by safety guard:") {
		reason := strings.TrimPrefix(pgDataDecision.Reason, "blocked by safety guard: ")
		storageSafetyGuardBlocks.WithLabelValues(reason).Inc()
	}
	if pgDataDecision.ShouldResize {
		if err := c.actor.ResizePGData(ctx, pgDataDecision.NewSize); err != nil {
			slog.Error("pgdata resize failed",
				"old_size", pgDataDecision.OldSize,
				"new_size", pgDataDecision.NewSize,
				"err", err,
			)
		} else {
			c.lastPGDataResizeAt = time.Now()
			storageResizeTotal.WithLabelValues("pgdata", string(pgDataDecision.TriggerType)).Inc()
			storageLastResizeTimestamp.WithLabelValues("pgdata").Set(float64(time.Now().Unix()))
			slog.Info("pgdata resized",
				"old_size", pgDataDecision.OldSize,
				"new_size", pgDataDecision.NewSize,
				"reason", pgDataDecision.Reason,
			)
			dec := pgDataDecision
			go func() {
				confirmCtx, cancel := context.WithTimeout(c.rootCtx, 10*time.Minute)
				defer cancel()
				latency, err := c.actor.WaitForPVCExpansion(confirmCtx, "PG_DATA", dec.NewSize)
				if err != nil {
					slog.Warn("pgdata pvc expansion confirmation failed", "err", err)
					return
				}
				slog.Info("pgdata pvc expansion confirmed",
					"old_size", dec.OldSize,
					"new_size", dec.NewSize,
					"resize_latency_s", latency.Seconds(),
				)
				storageResizeLatency.WithLabelValues("pgdata").Observe(latency.Seconds())
			}()
		}
	}

	// Decide + Act: WAL
	walDecision := c.decider.DecideWAL(snap, c.cfg.WAL, c.cfg.SafetyGuards, c.lastWALResizeAt)
	slog.Info("wal decision",
		"should_resize", walDecision.ShouldResize,
		"old_size", walDecision.OldSize,
		"new_size", walDecision.NewSize,
		"reason", walDecision.Reason,
	)
	if strings.HasPrefix(walDecision.Reason, "blocked by safety guard:") {
		reason := strings.TrimPrefix(walDecision.Reason, "blocked by safety guard: ")
		storageSafetyGuardBlocks.WithLabelValues(reason).Inc()
	}
	if walDecision.ShouldResize {
		if err := c.actor.ResizeWAL(ctx, walDecision.NewSize); err != nil {
			slog.Error("wal resize failed",
				"old_size", walDecision.OldSize,
				"new_size", walDecision.NewSize,
				"err", err,
			)
		} else {
			c.lastWALResizeAt = time.Now()
			storageResizeTotal.WithLabelValues("wal", string(walDecision.TriggerType)).Inc()
			storageLastResizeTimestamp.WithLabelValues("wal").Set(float64(time.Now().Unix()))
			slog.Info("wal resized",
				"old_size", walDecision.OldSize,
				"new_size", walDecision.NewSize,
				"reason", walDecision.Reason,
			)
			dec := walDecision
			go func() {
				confirmCtx, cancel := context.WithTimeout(c.rootCtx, 10*time.Minute)
				defer cancel()
				latency, err := c.actor.WaitForPVCExpansion(confirmCtx, "PG_WAL", dec.NewSize)
				if err != nil {
					slog.Warn("wal pvc expansion confirmation failed", "err", err)
					return
				}
				slog.Info("wal pvc expansion confirmed",
					"old_size", dec.OldSize,
					"new_size", dec.NewSize,
					"resize_latency_s", latency.Seconds(),
				)
				storageResizeLatency.WithLabelValues("wal").Observe(latency.Seconds())
			}()
		}
	}

	return nil
}
