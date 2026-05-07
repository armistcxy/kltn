package storage

import "github.com/prometheus/client_golang/prometheus"

var (
	pgdataUsagePercent = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "storage_pgdata_usage_percent",
		Help: "Current PGDATA PVC usage as percentage (0-100).",
	})
	pgdataTimeToFullHours = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "storage_pgdata_time_to_full_hours",
		Help: "Worst-case estimated time until PGDATA is full (hours). +Inf when not filling.",
	})
	walUsagePercent = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "storage_wal_usage_percent",
		Help: "Current WAL PVC usage as percentage (0-100).",
	})

	storageResizeTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "storage_resize_total",
		Help: "Total number of storage resize operations initiated.",
	}, []string{"target", "trigger_type"})

	storageLastResizeTimestamp = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "storage_last_resize_timestamp_seconds",
		Help: "Unix timestamp of the last storage resize patch (seconds).",
	}, []string{"target"})

	// storageResizeLatency is updated asynchronously when PVC expansion is confirmed.
	storageResizeLatency = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "storage_resize_latency_seconds",
		Help:    "Time from CNPG CR patch to PVC status.capacity.storage confirmed (seconds).",
		Buckets: []float64{5, 15, 30, 60, 120, 300, 600},
	}, []string{"target"})

	// pgdataRiskWindowSeconds measures the risk window per resize event: seconds from the most
	// recent ScaleUpThreshold upward crossing to the moment PVC expansion is confirmed.
	// Observes 0 when preemptive fired before threshold was ever crossed (no risk window).
	pgdataRiskWindowSeconds = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "storage_pgdata_risk_window_seconds",
		Help:    "Seconds from last ScaleUpThreshold crossing to PVC resize completion. 0 when preemptive fired before threshold.",
		Buckets: []float64{0, 10, 30, 60, 120, 300, 600},
	})

	storageSafetyGuardBlocks = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "storage_safety_guard_blocks_total",
		Help: "Number of times a safety guard blocked a storage resize.",
	}, []string{"reason"})
)

func init() {
	prometheus.MustRegister(
		pgdataUsagePercent,
		pgdataTimeToFullHours,
		walUsagePercent,
		storageResizeTotal,
		storageLastResizeTimestamp,
		storageResizeLatency,
		storageSafetyGuardBlocks,
		pgdataRiskWindowSeconds,
	)
}
