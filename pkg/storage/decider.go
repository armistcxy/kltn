package storage

import (
	"fmt"
	"math"
	"time"

	"k8s.io/apimachinery/pkg/api/resource"
)

type Decider struct{}

func NewDecider() *Decider { return &Decider{} }

func (d *Decider) DecidePGData(snap *StorageSnapshot, cfg PGDataConfig, guards SafetyGuardsConfig, lastResizeAt time.Time) StorageDecision {
	usage := snap.PGDataUsagePercent
	if math.IsNaN(usage) {
		return StorageDecision{
			Target: ResizeTargetPGData,
			Reason: "pgdata usage metric unavailable",
		}
	}

	decision := StorageDecision{
		Target:  ResizeTargetPGData,
		OldSize: snap.CurrentPGDataSize,
	}

	if block, reason := checkSafetyGuards(snap, guards); block {
		decision.Reason = "blocked by safety guard: " + reason
		return decision
	}

	critical := usage >= cfg.CriticalThresholdPercent
	aboveThreshold := usage >= cfg.ScaleUpThresholdPercent
	preemptive := isPreemptiveNeeded(snap.PGDataTimeToFullSeconds, cfg.PreemptiveResizeIfFullInHours)

	if propagating, propReason := isResizePropagating(snap); propagating && (critical || aboveThreshold) {
		if !preemptive {
			decision.Reason = "propagating: " + propReason
			return decision
		}
		critical = false
		aboveThreshold = false
		_ = propReason
	}

	if !aboveThreshold && !preemptive {
		decision.Reason = fmt.Sprintf("pgdata usage %.1f%% is below threshold %.1f%%", usage, cfg.ScaleUpThresholdPercent)
		return decision
	}

	if !critical && !lastResizeAt.IsZero() && time.Since(lastResizeAt) < cfg.Cooldown {
		remaining := cfg.Cooldown - time.Since(lastResizeAt)
		decision.Reason = fmt.Sprintf("cooldown active (next resize in %v)", remaining.Round(time.Second))
		return decision
	}

	newSize, err := computeNewSize(snap.CurrentPGDataSize, cfg.StepPercent, cfg.MaxSizeGi)
	if err != nil {
		decision.Reason = fmt.Sprintf("failed to compute new pgdata size: %v", err)
		return decision
	}
	if newSize == "" {
		decision.Reason = fmt.Sprintf("pgdata already at max size %dGi", cfg.MaxSizeGi)
		return decision
	}

	decision.ShouldResize = true
	decision.SkipCooldown = critical
	decision.NewSize = newSize

	switch {
	case critical:
		decision.TriggerType = TriggerTypeCritical
		decision.Reason = fmt.Sprintf(
			"critical: pgdata usage %.1f%% >= %.1f%% - resize %s -> %s (bypassing cooldown)",
			usage, cfg.CriticalThresholdPercent, snap.CurrentPGDataSize, newSize,
		)
	case preemptive:
		decision.TriggerType = TriggerTypePreemptive
		ttfHours := snap.PGDataTimeToFullSeconds / 3600
		decision.Reason = fmt.Sprintf(
			"preemptive: worst-case time-to-full %.1fh < threshold %.1fh (p95/p99 growth rate) - resize %s -> %s",
			ttfHours, cfg.PreemptiveResizeIfFullInHours, snap.CurrentPGDataSize, newSize,
		)
	default:
		decision.TriggerType = TriggerTypeReactive
		decision.Reason = fmt.Sprintf(
			"pgdata usage %.1f%% >= %.1f%% - resize %s -> %s",
			usage, cfg.ScaleUpThresholdPercent, snap.CurrentPGDataSize, newSize,
		)
	}

	return decision
}

func (d *Decider) DecideWAL(snap *StorageSnapshot, cfg WALConfig, guards SafetyGuardsConfig, lastResizeAt time.Time) StorageDecision {
	decision := StorageDecision{
		Target:  ResizeTargetWAL,
		OldSize: snap.CurrentWALSize,
	}

	if !cfg.Enabled {
		decision.Reason = "wal storage scaling disabled"
		return decision
	}

	if snap.CurrentWALSize == "" {
		decision.Reason = "cluster has no dedicated walStorage - skipping"
		return decision
	}

	ratio := snap.WALUsageRatio
	if math.IsNaN(ratio) {
		decision.Reason = "wal usage metric unavailable"
		return decision
	}
	usagePct := ratio * 100

	if block, reason := checkSafetyGuards(snap, guards); block {
		decision.Reason = "blocked by safety guard: " + reason
		return decision
	}

	critical := usagePct >= cfg.CriticalThresholdPercent
	aboveThreshold := usagePct >= cfg.ScaleUpThresholdPercent

	if !aboveThreshold {
		decision.Reason = fmt.Sprintf("wal usage %.1f%% is below threshold %.1f%%", usagePct, cfg.ScaleUpThresholdPercent)
		return decision
	}
	if !critical && !lastResizeAt.IsZero() && time.Since(lastResizeAt) < cfg.Cooldown {
		remaining := cfg.Cooldown - time.Since(lastResizeAt)
		decision.Reason = fmt.Sprintf("cooldown active (next wal resize in %v)", remaining.Round(time.Second))
		return decision
	}

	newSize, err := computeNewSize(snap.CurrentWALSize, cfg.StepPercent, cfg.MaxSizeGi)
	if err != nil {
		decision.Reason = fmt.Sprintf("failed to compute new wal size: %v", err)
		return decision
	}
	if newSize == "" {
		decision.Reason = fmt.Sprintf("wal already at max size %dGi", cfg.MaxSizeGi)
		return decision
	}

	decision.ShouldResize = true
	decision.SkipCooldown = critical
	decision.NewSize = newSize

	if critical {
		decision.TriggerType = TriggerTypeCritical
		decision.Reason = fmt.Sprintf(
			"critical: wal usage %.1f%% >= %.1f%% - resize %s -> %s (bypassing cooldown)",
			usagePct, cfg.CriticalThresholdPercent, snap.CurrentWALSize, newSize,
		)
	} else {
		decision.TriggerType = TriggerTypeReactive
		decision.Reason = fmt.Sprintf(
			"wal usage %.1f%% >= %.1f%% - resize %s -> %s",
			usagePct, cfg.ScaleUpThresholdPercent, snap.CurrentWALSize, newSize,
		)
	}

	return decision
}

func isPreemptiveNeeded(timeToFullSeconds, thresholdHours float64) bool {
	if thresholdHours <= 0 {
		return false
	}
	if math.IsNaN(timeToFullSeconds) || math.IsInf(timeToFullSeconds, 1) {
		return false
	}
	return timeToFullSeconds > 0 && timeToFullSeconds < thresholdHours*3600
}

func checkSafetyGuards(snap *StorageSnapshot, guards SafetyGuardsConfig) (bool, string) {
	if guards.BlockIfWALArchivePending > 0 && !math.IsNaN(snap.WALArchivePending) {
		if snap.WALArchivePending > float64(guards.BlockIfWALArchivePending) {
			return true, fmt.Sprintf(
				"WAL archive pending %.0f > limit %d (archiving may be broken)",
				snap.WALArchivePending, guards.BlockIfWALArchivePending,
			)
		}
	}

	if guards.BlockIfReplicationLagSeconds > 0 && !math.IsNaN(snap.ReplicationLagSeconds) {
		if snap.ReplicationLagSeconds > guards.BlockIfReplicationLagSeconds {
			return true, fmt.Sprintf(
				"replication lag %.1fs > limit %.1fs",
				snap.ReplicationLagSeconds, guards.BlockIfReplicationLagSeconds,
			)
		}
	}

	return false, ""
}

func computeNewSize(currentSizeStr string, stepPercent float64, maxSizeGi int) (string, error) {
	if currentSizeStr == "" {
		return "", fmt.Errorf("current size is empty")
	}

	q, err := resource.ParseQuantity(currentSizeStr)
	if err != nil {
		return "", fmt.Errorf("parse current size %q: %w", currentSizeStr, err)
	}

	currentBytes := q.Value()

	const gib = int64(1 << 30)
	currentGi := (currentBytes + gib - 1) / gib
	if currentGi >= int64(maxSizeGi) {
		return "", nil
	}

	newBytes := float64(currentBytes) * (1 + stepPercent/100)
	newGi := int64(math.Ceil(newBytes / float64(gib)))
	if newGi > int64(maxSizeGi) {
		newGi = int64(maxSizeGi)
	}
	if newGi <= currentGi {
		newGi = currentGi + 1
	}

	return fmt.Sprintf("%dGi", newGi), nil
}

func isResizePropagating(snap *StorageSnapshot) (bool, string) {
	if math.IsNaN(snap.PGDataCapacityBytes) || snap.PGDataCapacityBytes <= 0 {
		return false, ""
	}
	if snap.CurrentPGDataSize == "" {
		return false, ""
	}
	specQ, err := resource.ParseQuantity(snap.CurrentPGDataSize)
	if err != nil {
		return false, ""
	}
	specBytes := float64(specQ.Value())
	if specBytes > snap.PGDataCapacityBytes*1.05 {
		return true, fmt.Sprintf(
			"resize propagating: spec=%s (%.0f GiB) > kubelet capacity=%.1f GiB - metrics lagging, skipping decision",
			snap.CurrentPGDataSize, specBytes/float64(1<<30), snap.PGDataCapacityBytes/float64(1<<30),
		)
	}
	return false, ""
}
