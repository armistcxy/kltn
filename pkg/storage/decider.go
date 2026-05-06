package storage

import (
	"fmt"
	"math"
	"time"

	"k8s.io/apimachinery/pkg/api/resource"
)

// Decider encapsulates the storage scaling decision logic.
// It is stateless — all cooldown tracking happens in the Controller.
type Decider struct{}

// NewDecider creates a Decider.
func NewDecider() *Decider { return &Decider{} }

// DecidePGData evaluates whether the PGDATA volume should be resized.
// lastResizeAt is the time of the most recent PGDATA resize (zero if never resized).
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

	// Check safety guards first.
	if block, reason := checkSafetyGuards(snap, guards); block {
		decision.Reason = "blocked by safety guard: " + reason
		return decision
	}

	// Determine if a resize is warranted by threshold or preemptive signal.
	critical := usage >= cfg.CriticalThresholdPercent
	aboveThreshold := usage >= cfg.ScaleUpThresholdPercent
	preemptive := isPreemptiveNeeded(snap.PGDataTimeToFullSeconds, cfg.PreemptiveResizeIfFullInHours)

	// During resize propagation, kubelet usage% still reflects the old (smaller) capacity —
	// suppress threshold-based triggers to avoid cascading resizes on stale metrics.
	// Preemptive remains valid: observer.go already adjusts time-to-full using the spec size.
	if propagating, propReason := isResizePropagating(snap); propagating && (critical || aboveThreshold) {
		if !preemptive {
			decision.Reason = "propagating: " + propReason
			return decision
		}
		// Preemptive is active — suppress stale threshold flags but allow preemptive to proceed.
		critical = false
		aboveThreshold = false
		_ = propReason
	}

	if !aboveThreshold && !preemptive {
		decision.Reason = fmt.Sprintf("pgdata usage %.1f%% is below threshold %.1f%%", usage, cfg.ScaleUpThresholdPercent)
		return decision
	}

	// Respect cooldown unless in critical state.
	if !critical && !lastResizeAt.IsZero() && time.Since(lastResizeAt) < cfg.Cooldown {
		remaining := cfg.Cooldown - time.Since(lastResizeAt)
		decision.Reason = fmt.Sprintf("cooldown active (next resize in %v)", remaining.Round(time.Second))
		return decision
	}

	// Compute new size.
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
			"critical: pgdata usage %.1f%% >= %.1f%% — resize %s → %s (bypassing cooldown)",
			usage, cfg.CriticalThresholdPercent, snap.CurrentPGDataSize, newSize,
		)
	case preemptive:
		decision.TriggerType = TriggerTypePreemptive
		ttfHours := snap.PGDataTimeToFullSeconds / 3600
		decision.Reason = fmt.Sprintf(
			"preemptive: worst-case time-to-full %.1fh < threshold %.1fh (p95/p99 growth rate) — resize %s → %s",
			ttfHours, cfg.PreemptiveResizeIfFullInHours, snap.CurrentPGDataSize, newSize,
		)
	default:
		decision.TriggerType = TriggerTypeReactive
		decision.Reason = fmt.Sprintf(
			"pgdata usage %.1f%% >= %.1f%% — resize %s → %s",
			usage, cfg.ScaleUpThresholdPercent, snap.CurrentPGDataSize, newSize,
		)
	}

	return decision
}

// DecideWAL evaluates whether the WAL volume should be resized.
// lastResizeAt is the time of the most recent WAL resize (zero if never resized).
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
		decision.Reason = "cluster has no dedicated walStorage — skipping"
		return decision
	}

	// WAL usage ratio: 0–1 (or NaN if unavailable).
	ratio := snap.WALUsageRatio
	if math.IsNaN(ratio) {
		decision.Reason = "wal usage metric unavailable"
		return decision
	}
	usagePct := ratio * 100

	// Check safety guards.
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

	// Respect cooldown unless in critical state.
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
			"critical: wal usage %.1f%% >= %.1f%% — resize %s → %s (bypassing cooldown)",
			usagePct, cfg.CriticalThresholdPercent, snap.CurrentWALSize, newSize,
		)
	} else {
		decision.TriggerType = TriggerTypeReactive
		decision.Reason = fmt.Sprintf(
			"wal usage %.1f%% >= %.1f%% — resize %s → %s",
			usagePct, cfg.ScaleUpThresholdPercent, snap.CurrentWALSize, newSize,
		)
	}

	return decision
}

// isPreemptiveNeeded returns true when the worst-case time-to-full is positive and falls
// below the configured threshold. A NaN or infinite time-to-full means disk is not filling
// (growth rate ≤ 0) and preemptive resize is not needed.
// thresholdHours == 0 disables preemptive resizing.
func isPreemptiveNeeded(timeToFullSeconds, thresholdHours float64) bool {
	if thresholdHours <= 0 {
		return false
	}
	if math.IsNaN(timeToFullSeconds) || math.IsInf(timeToFullSeconds, 1) {
		return false
	}
	return timeToFullSeconds > 0 && timeToFullSeconds < thresholdHours*3600
}

// checkSafetyGuards returns (true, reason) if any safety guard blocks scaling.
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

// computeNewSize computes the new storage size string given the current size, step percent, and max GiB cap.
// Returns ("", nil) if the current size is already at or above maxSizeGi.
// The new size is always rounded up to the nearest GiB.
func computeNewSize(currentSizeStr string, stepPercent float64, maxSizeGi int) (string, error) {
	if currentSizeStr == "" {
		return "", fmt.Errorf("current size is empty")
	}

	q, err := resource.ParseQuantity(currentSizeStr)
	if err != nil {
		return "", fmt.Errorf("parse current size %q: %w", currentSizeStr, err)
	}

	currentBytes := q.Value() // int64, bytes

	// Current size in GiB (rounded up to nearest GiB for comparison).
	const gib = int64(1 << 30)
	currentGi := (currentBytes + gib - 1) / gib

	if currentGi >= int64(maxSizeGi) {
		return "", nil // already at max
	}

	// New size = current * (1 + stepPercent/100), rounded up to nearest GiB.
	newBytes := float64(currentBytes) * (1 + stepPercent/100)
	newGi := int64(math.Ceil(newBytes / float64(gib)))

	// Enforce max.
	if newGi > int64(maxSizeGi) {
		newGi = int64(maxSizeGi)
	}

	// Don't shrink (safety: should not happen but guard anyway).
	if newGi <= currentGi {
		newGi = currentGi + 1
	}

	return fmt.Sprintf("%dGi", newGi), nil
}

// isResizePropagating returns (true, reason) when the CNPG spec size is larger than
// the kubelet-reported PVC capacity, meaning a previous resize has been applied to the
// Cluster CR but the filesystem expansion has not yet been reflected in Prometheus.
// In this window, usage% is computed against the old capacity and is artificially high —
// acting on it would produce redundant cascading resizes.
func isResizePropagating(snap *StorageSnapshot) (bool, string) {
	if math.IsNaN(snap.PGDataCapacityBytes) || snap.PGDataCapacityBytes <= 0 {
		return false, "" // metric unavailable — don't block
	}
	if snap.CurrentPGDataSize == "" {
		return false, ""
	}
	specQ, err := resource.ParseQuantity(snap.CurrentPGDataSize)
	if err != nil {
		return false, ""
	}
	specBytes := float64(specQ.Value())
	// Allow 5% tolerance for unit rounding (e.g. "18Gi" spec vs kubelet reporting 18*1024^3 exactly).
	if specBytes > snap.PGDataCapacityBytes*1.05 {
		return true, fmt.Sprintf(
			"resize propagating: spec=%s (%.0f GiB) > kubelet capacity=%.1f GiB — metrics lagging, skipping decision",
			snap.CurrentPGDataSize, specBytes/float64(1<<30), snap.PGDataCapacityBytes/float64(1<<30),
		)
	}
	return false, ""
}
