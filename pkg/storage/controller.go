package storage

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Controller is the storage autoscaling control loop: Observe -> Decide -> Act
type Controller struct {
	cfg      Config
	observer *Observer
	decider  *Decider
	actor    *Actor

	rootCtx            context.Context
	lastPGDataResizeAt time.Time
	lastWALResizeAt    time.Time

	mu sync.Mutex

	// tPGDataThresholdCrossed records when pgdata usage most recently crossed scaleUpThreshold
	tPGDataThresholdCrossed time.Time

	// pgdataAboveThreshold is the threshold state observed in the previous reconcile cycle
	pgdataAboveThreshold bool

	// pgDataRiskWindowTotalMs accumulates risk_window across all resize cycles (ms)
	pgDataRiskWindowTotalMs atomic.Int64

	// pgDataResizeCount and pgDataResizeLatencyTotalMs track resize latency for averaging
	pgDataResizeCount          atomic.Int64
	pgDataResizeLatencyTotalMs atomic.Int64
}

func NewController(cfg Config, observer *Observer, decider *Decider, actor *Actor) *Controller {
	return &Controller{
		cfg:      cfg,
		observer: observer,
		decider:  decider,
		actor:    actor,
	}
}

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

func (c *Controller) reconcileOnce(ctx context.Context) error {
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

	if !math.IsNaN(snap.PGDataUsagePercent) {
		currentlyAbove := snap.PGDataUsagePercent >= c.cfg.PGData.ScaleUpThresholdPercent
		c.mu.Lock()
		if currentlyAbove && !c.pgdataAboveThreshold {
			c.tPGDataThresholdCrossed = snap.At
			c.mu.Unlock()
			slog.Info("pgdata threshold crossed",
				"usage_pct", snap.PGDataUsagePercent,
				"threshold_pct", c.cfg.PGData.ScaleUpThresholdPercent,
				"at", snap.At,
			)
		} else {
			c.mu.Unlock()
		}
		c.pgdataAboveThreshold = currentlyAbove
	}

	if !math.IsNaN(snap.PGDataUsagePercent) {
		pgdataUsagePercent.Set(snap.PGDataUsagePercent)
	}
	if !math.IsNaN(snap.PGDataTimeToFullSeconds) {
		pgdataTimeToFullHours.Set(snap.PGDataTimeToFullSeconds / 3600)
	}
	if !math.IsNaN(snap.WALUsageRatio) {
		walUsagePercent.Set(snap.WALUsageRatio * 100)
	}

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

				tConfirmed := time.Now()

				c.mu.Lock()
				thresholdCrossedAt := c.tPGDataThresholdCrossed
				c.tPGDataThresholdCrossed = time.Time{}
				c.mu.Unlock()

				latencyMs := latency.Milliseconds()
				count := c.pgDataResizeCount.Add(1)
				c.pgDataResizeLatencyTotalMs.Add(latencyMs)
				avgLatencyS := float64(c.pgDataResizeLatencyTotalMs.Load()) / float64(count) / 1000

				var riskWindowS float64
				if !thresholdCrossedAt.IsZero() {
					riskWindowMs := tConfirmed.Sub(thresholdCrossedAt).Milliseconds()
					c.pgDataRiskWindowTotalMs.Add(riskWindowMs)
					riskWindowS = float64(riskWindowMs) / 1000
				}
				totalRiskWindowS := float64(c.pgDataRiskWindowTotalMs.Load()) / 1000

				slog.Info("pgdata pvc expansion confirmed",
					"old_size", dec.OldSize,
					"new_size", dec.NewSize,
					"resize_latency_s", latency.Seconds(),
					"avg_resize_latency_s", avgLatencyS,
					"risk_window_s", riskWindowS,
					"risk_window_total_s", totalRiskWindowS,
				)
				storageResizeLatency.WithLabelValues("pgdata").Observe(latency.Seconds())
				pgdataRiskWindowSeconds.Observe(riskWindowS)
			}()
		}
	}

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
