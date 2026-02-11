package prometheusquery

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/client_golang/api"
	v1 "github.com/prometheus/client_golang/api/prometheus/v1"

	"github.com/prometheus/common/model"
)

// PrometheusQuerier is a wrapper around the Prometheus v1 API client.
type PrometheusQuerier struct {
	v1.API
}

func NewPrometheusQuerier(addr string) (*PrometheusQuerier, error) {
	ptClient, err := api.NewClient(api.Config{
		Address: addr,
	})
	if err != nil {
		return nil, err
	}

	return &PrometheusQuerier{
		v1.NewAPI(ptClient),
	}, nil
}

// GetCPUUsageByPod queries CPU usage for each pod.
//
// Metric used:
// node_namespace_pod_container:container_cpu_usage_seconds_total:sum_irate
//
// This metric is usually "cores used" (CPU seconds per second).
// Example output:
// - 0.05  => ~5% of 1 core
// - 1.20  => ~120% (1.2 cores)
func (q *PrometheusQuerier) GetCPUUsageByPod(
	ctx context.Context,
	namespace string,
	cluster string,
) (map[string]float64, error) {
	query := fmt.Sprintf(
		`sum(node_namespace_pod_container:container_cpu_usage_seconds_total:sum_irate{namespace=~"%s", pod=~"%s-.*"}) by (pod)`,
		namespace,
		cluster,
	)

	result, warnings, err := q.Query(ctx, query, time.Now())
	if err != nil {
		return nil, fmt.Errorf("prometheus query failed: %w", err)
	}

	if len(warnings) > 0 {
		slog.Warn("prometheus warnings when querying CPU usage by pod", "warnings", concatenateWarnings(warnings))
	}

	vector, ok := result.(model.Vector)
	if !ok {
		return nil, fmt.Errorf("unexpected result type %T, expected model.Vector", result)
	}

	out := make(map[string]float64, len(vector))

	for _, sample := range vector {
		pod := string(sample.Metric["pod"])
		if pod == "" {
			pod = string(sample.Metric["instance"])
		}

		out[pod] = float64(sample.Value)
	}

	return out, nil
}

// GetBackendsByPod queries current backend connections for each Postgres instance (pod)
// in a CNPG cluster.
//
// PromQL used:
// sum by (pod) (cnpg_backends_total{namespace="...", cluster="..."})
//
// Return example:
// map["pg-cluster-1"] = 23.0000
// map["pg-cluster-2"] = 19.0000
func (q *PrometheusQuerier) GetBackendsByPod(
	ctx context.Context,
	namespace, cluster string,
) (map[string]float64, error) {
	// PromQL query:
	// - cnpg_backends_total: number of backend connections
	// - sum by (pod): ensure we get 1 value per pod
	query := fmt.Sprintf(
		`sum by (pod) (cnpg_backends_total{namespace=~"%s", pod=~"%s-.*"})`,
		namespace,
		cluster,
	)

	// Query at the current time
	result, warnings, err := q.Query(ctx, query, time.Now())
	if err != nil {
		return nil, fmt.Errorf("prometheus query failed: %w", err)
	}

	// Warnings are non-fatal, log them for visibility
	if len(warnings) > 0 {
		slog.Warn("prometheus warnings when querying backends by pod", "warnings", concatenateWarnings(warnings))
	}

	// Result is a vector of values, one per pod
	vector, ok := result.(model.Vector)
	if !ok {
		return nil, fmt.Errorf("unexpected result type: %T, expected model.Vector", result)
	}

	out := make(map[string]float64, len(vector))
	for _, sample := range vector {
		pod := string(sample.Metric["pod"])
		if pod == "" {
			pod = string(sample.Metric["instance"])
		}
		value := float64(sample.Value)
		out[pod] = value
	}

	return out, nil
}

// GetMemoryUsageByPod queries Memory usage for each pod (return in bytes).
//
// PromQL used:
// sum(container_memory_working_set_bytes{pod=~"$instances", namespace="$namespace", container!="", image!=""}) by (pod)
func (q *PrometheusQuerier) GetMemoryUsageByPod(
	ctx context.Context,
	namespace string,
	instances string,
) (map[string]float64, error) {
	query := fmt.Sprintf(
		`sum(container_memory_working_set_bytes{pod=~"%s", namespace="%s", container!="", image!=""}) by (pod)`,
		instances,
		namespace,
	)

	result, warnings, err := q.Query(ctx, query, time.Now())
	if err != nil {
		return nil, fmt.Errorf("prometheus query failed: %w", err)
	}

	if len(warnings) > 0 {
		slog.Warn("prometheus warnings when querying memory usage by pod", "warnings", concatenateWarnings(warnings))
	}

	vector, ok := result.(model.Vector)
	if !ok {
		return nil, fmt.Errorf("unexpected result type %T, expected model.Vector", result)
	}

	out := make(map[string]float64, len(vector))
	for _, sample := range vector {
		pod := string(sample.Metric["pod"])
		if pod == "" {
			pod = string(sample.Metric["instance"])
		}
		out[pod] = float64(sample.Value)
	}

	return out, nil
}

// GetTPSByPod queries Transactions Per Second (TPS) for each pod.
//
// PromQL used:
// sum by (pod) (
//
//	rate(cnpg_pg_stat_database_xact_commit{namespace=~"$namespace", pod=~"$instances"}[1m])
//
// )
// +
// sum by (pod) (
//
//	rate(cnpg_pg_stat_database_xact_rollback{namespace=~"$namespace", pod=~"$instances"}[1m])
//
// )
func (q *PrometheusQuerier) GetTPSByPod(
	ctx context.Context,
	namespace string,
	instances string,
	interval time.Duration,
) (map[string]float64, error) {
	intervalStr := interval.String()
	query := fmt.Sprintf(
		`sum by (pod) (
			rate(cnpg_pg_stat_database_xact_commit{namespace=~"%s", pod=~"%s"}[%s])
		)
		+
		sum by (pod) (
			rate(cnpg_pg_stat_database_xact_rollback{namespace=~"%s", pod=~"%s"}[%s])
		)`,
		namespace,
		instances,
		intervalStr,
		namespace,
		instances,
		intervalStr,
	)

	result, warnings, err := q.Query(ctx, query, time.Now())
	if err != nil {
		return nil, fmt.Errorf("prometheus query failed: %w", err)
	}

	if len(warnings) > 0 {
		slog.Warn("prometheus warnings when querying TPS by pod", "warnings", concatenateWarnings(warnings))
	}

	vector, ok := result.(model.Vector)
	if !ok {
		return nil, fmt.Errorf("unexpected result type %T, expected model.Vector", result)
	}

	out := make(map[string]float64, len(vector))
	for _, sample := range vector {
		pod := string(sample.Metric["pod"])
		if pod == "" {
			pod = string(sample.Metric["instance"])
		}
		out[pod] = float64(sample.Value)
	}

	return out, nil
}

func concatenateWarnings(warnings v1.Warnings) string {
	var b strings.Builder

	total := 0
	for _, w := range warnings {
		total += len(w) + 32
	}
	b.Grow(total)

	for i, w := range warnings {
		b.WriteString("Warning ")
		b.WriteString(strconv.Itoa(i + 1))
		b.WriteString(": ")
		b.WriteString(w)
		b.WriteByte('\n')
	}

	return b.String()
}
