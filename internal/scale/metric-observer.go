package scale

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	prometheusquery "github.com/armistcxy/kltn/pkg/prometheus-query"
)

// PrometheusMetricsObserver implements MetricsObserver by querying Prometheus.
type PrometheusMetricsObserver struct {
	querier   *prometheusquery.PrometheusQuerier
	namespace string
	cluster   string
	interval  time.Duration
}

// NewPrometheusMetricsObserver creates a new observer that fetches metrics from Prometheus.
func NewPrometheusMetricsObserver(
	querier *prometheusquery.PrometheusQuerier,
	namespace string,
	cluster string,
	interval time.Duration,
) *PrometheusMetricsObserver {
	return &PrometheusMetricsObserver{
		querier:   querier,
		namespace: namespace,
		cluster:   cluster,
		interval:  interval,
	}
}

// Observe gathers a snapshot of all metrics from Prometheus.
func (o *PrometheusMetricsObserver) Observe(ctx context.Context) (*MetricsSnapshot, error) {
	snapshot := &MetricsSnapshot{
		At: time.Now(),
	}

	// Query all metrics in parallel
	cpuChan := make(chan map[string]float64, 1)
	backendsChan := make(chan map[string]float64, 1)
	memoryChan := make(chan map[string]float64, 1)
	tpsChan := make(chan map[string]float64, 1)
	errChan := make(chan error, 4)

	// Query CPU usage by pod
	go func() {
		cpu, err := o.querier.GetCPUUsageByPod(ctx, o.namespace, o.cluster)
		if err != nil {
			errChan <- fmt.Errorf("failed to get CPU usage: %w", err)
			return
		}
		cpuChan <- cpu
	}()

	// Query backends by pod
	go func() {
		backends, err := o.querier.GetBackendsByPod(ctx, o.namespace, o.cluster)
		if err != nil {
			errChan <- fmt.Errorf("failed to get backends: %w", err)
			return
		}
		backendsChan <- backends
	}()

	// Query memory usage by pod
	go func() {
		memory, err := o.querier.GetMemoryUsageByPod(ctx, o.namespace, o.cluster+"-.*")
		if err != nil {
			errChan <- fmt.Errorf("failed to get memory usage: %w", err)
			return
		}
		memoryChan <- memory
	}()

	// Query TPS by pod
	go func() {
		tps, err := o.querier.GetTPSByPod(ctx, o.namespace, o.cluster+"-.*", o.interval)
		if err != nil {
			errChan <- fmt.Errorf("failed to get TPS: %w", err)
			return
		}
		tpsChan <- tps
	}()

	// Wait for all queries to complete
	successCount := 0
	for i := 0; i < 4; i++ {
		select {
		case cpu := <-cpuChan:
			snapshot.CPUByPod = cpu
			successCount++
		case backends := <-backendsChan:
			snapshot.BackendsByPod = backends
			successCount++
		case memory := <-memoryChan:
			snapshot.MemoryByPod = memory
			successCount++
		case tps := <-tpsChan:
			snapshot.TPSByPod = tps
			successCount++
		case err := <-errChan:
			slog.Error("metric query error", "error", err)
		}
	}

	if successCount < 4 {
		return nil, fmt.Errorf("failed to retrieve all metrics: only %d/4 successful", successCount)
	}

	// Calculate aggregate metrics
	snapshot.TotalBackends = aggregateSum(snapshot.BackendsByPod)
	snapshot.AvgCPU, snapshot.MaxCPU = aggregateAvgAndMax(snapshot.CPUByPod)
	snapshot.AvgMemory, snapshot.MaxMemory = aggregateAvgAndMax(snapshot.MemoryByPod)
	snapshot.TotalTPS = aggregateSum(snapshot.TPSByPod)

	return snapshot, nil
}

// aggregateSum returns the sum of all values in a map.
func aggregateSum(data map[string]float64) float64 {
	sum := 0.0
	for _, v := range data {
		sum += v
	}
	return sum
}

// aggregateAvgAndMax returns the average and maximum of all values in a map.
func aggregateAvgAndMax(data map[string]float64) (avg, max float64) {
	if len(data) == 0 {
		return 0, 0
	}

	sum := 0.0
	max = 0.0

	for _, v := range data {
		sum += v
		if v > max {
			max = v
		}
	}

	avg = sum / float64(len(data))
	return avg, max
}
