package storage

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"sync"
	"time"

	prometheusquery "github.com/armistcxy/kltn/pkg/prometheus-query"
	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/types"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// Observer collects disk-related metrics from Prometheus and the Kubernetes API.
type Observer struct {
	querier   *prometheusquery.PrometheusQuerier
	k8sClient ctrlclient.Client
}

func NewObserver(querier *prometheusquery.PrometheusQuerier, k8sClient ctrlclient.Client) *Observer {
	return &Observer{
		querier:   querier,
		k8sClient: k8sClient,
	}
}

func (o *Observer) Observe(ctx context.Context, cfg Config) (*StorageSnapshot, error) {
	type queryResult struct {
		name  string
		value float64
		err   error
	}

	pc := cfg.PGData
	consumptionLong := fmt.Sprintf(
		`-deriv(kubelet_volume_stats_available_bytes{namespace="%s",persistentvolumeclaim=~"%s-[0-9]+"}[%s])`,
		cfg.Namespace, cfg.Cluster, promDuration(pc.LongTermDerivWindow),
	)
	consumptionShort := fmt.Sprintf(
		`-deriv(kubelet_volume_stats_available_bytes{namespace="%s",persistentvolumeclaim=~"%s-[0-9]+"}[%s])`,
		cfg.Namespace, cfg.Cluster, promDuration(pc.ShortTermDerivWindow),
	)

	queries := map[string]string{
		"pgdata_usage_pct": fmt.Sprintf(
			`max((1 - kubelet_volume_stats_available_bytes{namespace="%s",persistentvolumeclaim=~"%s-[0-9]+"} / kubelet_volume_stats_capacity_bytes{namespace="%s",persistentvolumeclaim=~"%s-[0-9]+"}) * 100)`,
			cfg.Namespace, cfg.Cluster, cfg.Namespace, cfg.Cluster,
		),
		"pgdata_available_bytes": fmt.Sprintf(
			`min(kubelet_volume_stats_available_bytes{namespace="%s",persistentvolumeclaim=~"%s-[0-9]+"})`,
			cfg.Namespace, cfg.Cluster,
		),
		"pgdata_capacity_bytes": fmt.Sprintf(
			`min(kubelet_volume_stats_capacity_bytes{namespace="%s",persistentvolumeclaim=~"%s-[0-9]+"})`,
			cfg.Namespace, cfg.Cluster,
		),
		// Worst-case growth rate: max of p95 long-term trend and p99 short-term spike.
		"pgdata_worst_case_growth": fmt.Sprintf(
			`max(quantile_over_time(0.95, (%s)[%s:%s]) or quantile_over_time(0.99, (%s)[%s:%s]))`,
			consumptionLong, promDuration(pc.LongTermQuantileWindow), promDuration(pc.LongTermSampleInterval),
			consumptionShort, promDuration(pc.ShortTermQuantileWindow), promDuration(pc.ShortTermSampleInterval),
		),
		"wal_usage_ratio": fmt.Sprintf(
			`max(cnpg_collector_pg_wal{value="size",namespace="%s"}) / max(cnpg_collector_pg_wal{value="volume_size",namespace="%s"})`,
			cfg.Namespace, cfg.Namespace,
		),
		"wal_archive_pending": fmt.Sprintf(
			`max(cnpg_collector_pg_wal_archive_status{value="ready",namespace="%s"})`,
			cfg.Namespace,
		),
		"db_growth_rate": fmt.Sprintf(
			`max(deriv(cnpg_pg_database_size_bytes{datname="app",namespace="%s"}[1h]))`,
			cfg.Namespace,
		),
		"replication_lag": fmt.Sprintf(
			`max(cnpg_collector_pg_replication_lag{namespace="%s"})`,
			cfg.Namespace,
		),
	}

	ch := make(chan queryResult, len(queries))
	var wg sync.WaitGroup
	for name, query := range queries {
		wg.Add(1)
		go func(name, query string) {
			defer wg.Done()
			val, err := o.querier.QueryScalar(ctx, query)
			ch <- queryResult{name: name, value: val, err: err}
		}(name, query)
	}
	wg.Wait()
	close(ch)

	results := make(map[string]float64, len(queries))
	for r := range ch {
		if r.err != nil {
			slog.Warn("storage metric query failed", "metric", r.name, "err", r.err)
			results[r.name] = math.NaN()
		} else {
			results[r.name] = r.value
		}
	}

	pgDataSize, walSize, err := o.currentSizes(ctx, cfg.Namespace, cfg.Cluster)
	if err != nil {
		return nil, fmt.Errorf("fetch current storage sizes: %w", err)
	}

	availableBytes := results["pgdata_available_bytes"]
	growthRate := results["pgdata_worst_case_growth"]

	effectiveAvailable := availableBytes
	capacityBytes := results["pgdata_capacity_bytes"]
	if !math.IsNaN(capacityBytes) && !math.IsNaN(availableBytes) && capacityBytes > 0 && pgDataSize != "" {
		if specQ, err := resource.ParseQuantity(pgDataSize); err == nil {
			specBytes := float64(specQ.Value())
			if specBytes > capacityBytes*1.05 {
				usedBytes := capacityBytes - availableBytes
				if adj := specBytes - usedBytes; adj > effectiveAvailable {
					effectiveAvailable = adj
					slog.Info("pgdata available adjusted for pending resize",
						"spec", pgDataSize,
						"kubelet_capacity_gi", capacityBytes/float64(1<<30),
						"raw_available_gi", availableBytes/float64(1<<30),
						"adjusted_available_gi", effectiveAvailable/float64(1<<30),
					)
				}
			}
		}
	}

	timeToFull := math.Inf(1) // default: disk not filling
	if !math.IsNaN(effectiveAvailable) && !math.IsNaN(growthRate) {
		if growthRate > 0 {
			timeToFull = effectiveAvailable / growthRate
		}
	} else {
		timeToFull = math.NaN()
	}

	snap := &StorageSnapshot{
		At:                                   time.Now(),
		PGDataUsagePercent:                   results["pgdata_usage_pct"],
		PGDataAvailableBytes:                 availableBytes,
		PGDataCapacityBytes:                  results["pgdata_capacity_bytes"],
		PGDataWorstCaseGrowthRateBytesPerSec: growthRate,
		PGDataTimeToFullSeconds:              timeToFull,
		WALUsageRatio:                        results["wal_usage_ratio"],
		WALArchivePending:                    results["wal_archive_pending"],
		DBSizeGrowthRateBytesPerSec:          results["db_growth_rate"],
		ReplicationLagSeconds:                results["replication_lag"],
		CurrentPGDataSize:                    pgDataSize,
		CurrentWALSize:                       walSize,
	}

	return snap, nil
}

func (o *Observer) currentSizes(ctx context.Context, namespace, cluster string) (pgDataSize, walSize string, err error) {
	var cl cnpgv1.Cluster
	if err = o.k8sClient.Get(ctx, types.NamespacedName{
		Namespace: namespace,
		Name:      cluster,
	}, &cl); err != nil {
		return "", "", fmt.Errorf("get CNPG cluster: %w", err)
	}

	pgDataSize = cl.Spec.StorageConfiguration.Size
	if cl.Spec.WalStorage != nil {
		walSize = cl.Spec.WalStorage.Size
	}
	return pgDataSize, walSize, nil
}

func promDuration(d time.Duration) string {
	secs := int64(d.Seconds())
	if secs <= 0 {
		secs = 1
	}
	return fmt.Sprintf("%ds", secs)
}
