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
	"k8s.io/apimachinery/pkg/types"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// Observer collects disk-related metrics from Prometheus and the Kubernetes API.
type Observer struct {
	querier   *prometheusquery.PrometheusQuerier
	k8sClient ctrlclient.Client
}

// NewObserver creates an Observer backed by the given Prometheus querier and Kubernetes client.
func NewObserver(querier *prometheusquery.PrometheusQuerier, k8sClient ctrlclient.Client) *Observer {
	return &Observer{
		querier:   querier,
		k8sClient: k8sClient,
	}
}

// Observe collects a StorageSnapshot by running all Prometheus queries in parallel
// and reading the current storage sizes from the CNPG Cluster CR.
func (o *Observer) Observe(ctx context.Context, cfg Config) (*StorageSnapshot, error) {
	type queryResult struct {
		name  string
		value float64
		err   error
	}

	// Consumption rate subexpression: rate at which available bytes decrease (bytes/sec, positive = filling).
	// Negate deriv(available) so that positive values represent disk being consumed.
	consumptionLong := fmt.Sprintf(
		`-deriv(kubelet_volume_stats_available_bytes{namespace="%s",persistentvolumeclaim=~"%s-[0-9]+"}[1h])`,
		cfg.Namespace, cfg.Cluster,
	)
	consumptionShort := fmt.Sprintf(
		`-deriv(kubelet_volume_stats_available_bytes{namespace="%s",persistentvolumeclaim=~"%s-[0-9]+"}[5m])`,
		cfg.Namespace, cfg.Cluster,
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
		// Worst-case growth rate: max of p95 long-term trend and p99 short-term spike.
		//   - p95 over 24 h sampled every 5 m captures sustained growth trends.
		//   - p99 over 6 h sampled every 1 m captures acute spikes (batch jobs, WAL bursts).
		// Taking the max of both means prepare for whichever scenario is worse.
		"pgdata_worst_case_growth": fmt.Sprintf(
			`max(quantile_over_time(0.95, (%s)[24h:5m]), quantile_over_time(0.99, (%s)[6h:1m]))`,
			consumptionLong, consumptionShort,
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
			// Non-fatal: log and treat as 0 / NaN
			// Missing metrics should not block scaling decisions
			slog.Warn("storage metric query failed", "metric", r.name, "err", r.err)
			results[r.name] = math.NaN()
		} else {
			results[r.name] = r.value
		}
	}

	// Fetch current storage sizes from the Cluster CR
	pgDataSize, walSize, err := o.currentSizes(ctx, cfg.Namespace, cfg.Cluster)
	if err != nil {
		return nil, fmt.Errorf("fetch current storage sizes: %w", err)
	}

	// Compute time-to-full from available bytes and worst-case growth rate.
	// growth_rate > 0 means disk is being consumed, <= 0 means disk is stable or shrinking.
	availableBytes := results["pgdata_available_bytes"]
	growthRate := results["pgdata_worst_case_growth"]
	timeToFull := math.Inf(1) // default: disk not filling
	if !math.IsNaN(availableBytes) && !math.IsNaN(growthRate) {
		if growthRate > 0 {
			timeToFull = availableBytes / growthRate
		}
	} else {
		timeToFull = math.NaN()
	}

	snap := &StorageSnapshot{
		At:                                   time.Now(),
		PGDataUsagePercent:                   results["pgdata_usage_pct"],
		PGDataAvailableBytes:                 availableBytes,
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

// currentSizes returns the current spec.storage.size and spec.walStorage.size
// from the CNPG Cluster CR. walSize is empty if spec.walStorage is not configured.
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
