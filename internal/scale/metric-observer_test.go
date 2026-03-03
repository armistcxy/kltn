package scale

import (
	"context"
	"os"
	"testing"
	"time"

	prometheusquery "github.com/armistcxy/kltn/pkg/prometheus-query"
)

// TestPrometheusMetricsObserver_Integration tests the observer against a real Prometheus instance.
// This test requires PROMETHEUS_ADDR environment variable to be set.
// Example: PROMETHEUS_ADDR="http://localhost:9090" go test ./internal/scale -v -run TestPrometheusMetricsObserver_Integration
func TestPrometheusMetricsObserver_IntegrationSimple(t *testing.T) {
	prometheusAddr := os.Getenv("PROMETHEUS_ADDR")
	if prometheusAddr == "" {
		t.Skip("PROMETHEUS_ADDR environment variable not set, skipping integration test")
	}

	querier, err := prometheusquery.NewPrometheusQuerier(prometheusAddr)
	if err != nil {
		t.Fatalf("failed to create prometheus querier: %v", err)
	}

	observer := NewPrometheusMetricsObserver(querier, "default", "pg-cluster", time.Minute)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	snapshot, err := observer.Observe(ctx)
	if err != nil {
		t.Fatalf("Observe() failed: %v", err)
	}

	// Verify snapshot was created with a timestamp
	if snapshot.At.IsZero() {
		t.Error("expected snapshot.At to be set")
	}

	// Log the observed metrics for debugging
	t.Logf("Snapshot timestamp: %v", snapshot.At)
	t.Logf("Total backends: %v", snapshot.TotalBackends)
	t.Logf("Avg CPU: %v, Max CPU: %v", snapshot.AvgCPU, snapshot.MaxCPU)
	t.Logf("Avg Memory: %v, Max Memory: %v", snapshot.AvgMemory, snapshot.MaxMemory)
	t.Logf("Total TPS: %v", snapshot.TotalTPS)
	t.Logf("CPU by pod: %v", snapshot.CPUByPod)
	t.Logf("Backends by pod: %v", snapshot.BackendsByPod)
	t.Logf("Memory by pod: %v", snapshot.MemoryByPod)
	t.Logf("TPS by pod: %v", snapshot.TPSByPod)
}

// TestPrometheusMetricsObserver_IntegrationWithTimeout tests observer behavior with context timeout.
// This test requires PROMETHEUS_ADDR environment variable to be set.
func TestPrometheusMetricsObserver_IntegrationWithTimeout(t *testing.T) {
	prometheusAddr := os.Getenv("PROMETHEUS_ADDR")
	if prometheusAddr == "" {
		t.Skip("PROMETHEUS_ADDR environment variable not set, skipping integration test")
	}

	querier, err := prometheusquery.NewPrometheusQuerier(prometheusAddr)
	if err != nil {
		t.Fatalf("failed to create prometheus querier: %v", err)
	}

	observer := NewPrometheusMetricsObserver(querier, "default", "pg-cluster", time.Minute)

	// Very short timeout to test context handling
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err = observer.Observe(ctx)
	if err == nil {
		// It's okay if it doesn't error on a fast connection, but it should be fast
		t.Log("Observe completed despite short timeout")
	} else {
		t.Logf("Observe timed out as expected: %v", err)
	}
}

// TestPrometheusMetricsObserver_IntegrationMultipleCalls tests multiple consecutive Observe calls.
// This test requires PROMETHEUS_ADDR environment variable to be set.
func TestPrometheusMetricsObserver_IntegrationMultipleCalls(t *testing.T) {
	prometheusAddr := os.Getenv("PROMETHEUS_ADDR")
	if prometheusAddr == "" {
		t.Skip("PROMETHEUS_ADDR environment variable not set, skipping integration test")
	}

	querier, err := prometheusquery.NewPrometheusQuerier(prometheusAddr)
	if err != nil {
		t.Fatalf("failed to create prometheus querier: %v", err)
	}

	observer := NewPrometheusMetricsObserver(querier, "default", "pg-cluster", time.Minute)

	ctx := context.Background()

	// First call
	snapshot1, err := observer.Observe(ctx)
	if err != nil {
		t.Fatalf("first Observe() failed: %v", err)
	}

	if snapshot1.At.IsZero() {
		t.Error("expected first snapshot.At to be set")
	}

	// Small delay
	time.Sleep(100 * time.Millisecond)

	// Second call
	snapshot2, err := observer.Observe(ctx)
	if err != nil {
		t.Fatalf("second Observe() failed: %v", err)
	}

	if snapshot2.At.IsZero() {
		t.Error("expected second snapshot.At to be set")
	}

	// Verify timestamps are different (or at least we get consistent behavior)
	t.Logf("First snapshot timestamp: %v", snapshot1.At)
	t.Logf("Second snapshot timestamp: %v", snapshot2.At)
	t.Logf("Time difference: %v", snapshot2.At.Sub(snapshot1.At))
}
