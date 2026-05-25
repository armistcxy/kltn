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

	PGDataUsagePercent float64
	WALUsageRatio      float64

	WALArchivePending           float64
	DBSizeGrowthRateBytesPerSec float64
	PGDataAvailableBytes        float64

	// PGDataWorstCaseGrowthRateBytesPerSec is the worst-case disk consumption rate in bytes/sec
	PGDataWorstCaseGrowthRateBytesPerSec float64

	// PGDataTimeToFullSeconds is available_bytes / worst_case_growth_rate
	PGDataTimeToFullSeconds float64

	ReplicationLagSeconds float64

	// PGDataCapacityBytes is the actual PVC capacity reported by kubelet (kubelet_volume_stats_capacity_bytes)
	PGDataCapacityBytes float64

	// WALCapacityBytes is the actual WAL PVC capacity reported by kubelet
	WALCapacityBytes float64

	// CurrentPGDataSize is the current spec.storage.size from the Cluster CR
	CurrentPGDataSize string

	// CurrentWALSize is the current spec.walStorage.size from the Cluster CR
	CurrentWALSize string
}

type ResizeTarget string

const (
	ResizeTargetNone   ResizeTarget = "none"
	ResizeTargetPGData ResizeTarget = "pgdata"
	ResizeTargetWAL    ResizeTarget = "wal"
)

type StorageDecision struct {
	Target       ResizeTarget
	ShouldResize bool
	SkipCooldown bool
	OldSize      string
	NewSize      string
	Reason       string
	TriggerType  TriggerType
}
