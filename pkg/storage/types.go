package storage

import "time"

// TriggerType classifies what caused a storage resize decision.
type TriggerType string

const (
	TriggerTypeNone       TriggerType = "none"
	TriggerTypeReactive   TriggerType = "reactive"
	TriggerTypeCritical   TriggerType = "critical"
	TriggerTypePreemptive TriggerType = "preemptive"
)

// StorageSnapshot is a point-in-time snapshot of disk-related metrics.
type StorageSnapshot struct {
	At time.Time

	// PGDataUsagePercent is the % of the PGDATA PVC(s) that is used.
	// Computed as (1 - available/capacity) * 100, taking the max across all PGDATA PVCs.
	PGDataUsagePercent float64

	// WALUsageRatio is cnpg_collector_pg_wal{value="size"} / cnpg_collector_pg_wal{value="volume_size"}.
	// Only meaningful when walStorage is a separate volume (WALStorage.Enabled = true).
	// Value in range [0, 1]. NaN if walStorage volume_size is 0 or absent.
	WALUsageRatio float64

	// WALArchivePending is the number of WAL segments waiting to be archived.
	// From cnpg_collector_pg_wal_archive_status{value="ready"}.
	WALArchivePending float64

	// DBSizeGrowthRateBytesPerSec is deriv(cnpg_pg_database_size_bytes[1h]).
	// Positive means the DB is growing. Informational only.
	DBSizeGrowthRateBytesPerSec float64

	// PGDataAvailableBytes is the current minimum available bytes across all PGDATA PVCs.
	// Used together with PGDataWorstCaseGrowthRateBytesPerSec to estimate time-to-full.
	PGDataAvailableBytes float64

	// PGDataWorstCaseGrowthRateBytesPerSec is the worst-case disk consumption rate in bytes/sec.
	// It is the max of:
	//   - p95 of hourly consumption rate over the last 24 h (captures long-term trend)
	//   - p99 of 5-minute consumption rate over the last 6 h (captures short-term spikes)
	// Positive means disk is being consumed. 0 or negative means disk is not filling.
	PGDataWorstCaseGrowthRateBytesPerSec float64

	// PGDataTimeToFullSeconds is available_bytes / worst_case_growth_rate.
	// math.Inf(1) when growth rate <= 0 (disk not filling).
	// NaN when either input metric was unavailable.
	PGDataTimeToFullSeconds float64

	// ReplicationLagSeconds is the maximum replication lag among all replicas.
	ReplicationLagSeconds float64

	// PGDataCapacityBytes is the actual PVC capacity reported by kubelet (kubelet_volume_stats_capacity_bytes).
	// This lags behind CurrentPGDataSize by up to ~2 min after a resize while the filesystem expands.
	// Used to detect "resize still propagating" and suppress stale-metric decisions.
	PGDataCapacityBytes float64

	// CurrentPGDataSize is the current spec.storage.size from the Cluster CR (e.g. "10Gi").
	CurrentPGDataSize string

	// CurrentWALSize is the current spec.walStorage.size from the Cluster CR. Empty if walStorage is not configured.
	CurrentWALSize string
}

// ResizeTarget describes which storage volume is being resized.
type ResizeTarget string

const (
	ResizeTargetNone   ResizeTarget = "none"
	ResizeTargetPGData ResizeTarget = "pgdata"
	ResizeTargetWAL    ResizeTarget = "wal"
)

// StorageDecision is the output of the Decider for one storage volume.
type StorageDecision struct {
	// Target identifies which volume this decision applies to.
	Target ResizeTarget

	// ShouldResize is true when a resize should be performed.
	ShouldResize bool

	// SkipCooldown is true when the critical threshold was crossed and
	// the cooldown window should be bypassed.
	SkipCooldown bool

	// OldSize is the current size string (e.g. "10Gi").
	OldSize string

	// NewSize is the desired size string after resize (e.g. "15Gi").
	// Empty when ShouldResize is false.
	NewSize string

	// Reason is a human-readable explanation of the decision.
	Reason string

	// TriggerType classifies what caused this decision (reactive, critical, preemptive, or none).
	TriggerType TriggerType
}
