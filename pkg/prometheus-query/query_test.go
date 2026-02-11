package prometheusquery

import (
	"context"
	"testing"
	"time"
)

func TestQueryBackendsByPod(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second*10)
	defer cancel()

	promAddr := "http://localhost:9090"
	querier, err := NewPrometheusQuerier(promAddr)
	if err != nil {
		t.Fatalf("failed to create Prometheus querier: %v", err)
	}

	namespace := "default"
	cluster := "pg-cluster"
	results, err := querier.GetBackendsByPod(ctx, namespace, cluster)
	if err != nil {
		t.Fatalf("failed to query backends by pod: %v", err)
	}
	if len(results) == 0 {
		t.Fatalf("expected non-empty results, got empty")
	}

	for pod, backends := range results {
		t.Logf("Pod: %s, Backends: %f", pod, backends)
	}
}

func TestQueryCPUUsageByPod(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second*10)
	defer cancel()
	promAddr := "http://localhost:9090"
	querier, err := NewPrometheusQuerier(promAddr)
	if err != nil {
		t.Fatalf("failed to create Prometheus querier: %v", err)
	}

	namespace := "default"
	cluster := "pg-cluster"
	results, err := querier.GetCPUUsageByPod(ctx, namespace, cluster)
	if err != nil {
		t.Fatalf("failed to query CPU usage by pod: %v", err)
	}

	if len(results) == 0 {
		t.Fatalf("expected non-empty results, got empty")
	}

	for pod, cpuUsage := range results {
		t.Logf("Pod: %s, CPU Usage: %f", pod, cpuUsage)
	}
}
