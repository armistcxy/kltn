package scale

import (
	"context"
	"os"
	"testing"
	"time"

	prometheusquery "github.com/armistcxy/kltn/pkg/prometheus-query"
)

func testSpecs() []MetricSpec {
	return []MetricSpec{
		{
			Name:               "backends",
			Query:              `sum(cnpg_backends_total{namespace="default", pod=~"pg-cluster-.*"})`,
			ScaleUpThreshold:   80,
			ScaleDownThreshold: 20,
		},
		{
			Name:               "tps",
			Query:              `sum(rate(cnpg_pg_stat_database_xact_commit{namespace="default", pod=~"pg-cluster-.*"}[1m]))`,
			ScaleUpThreshold:   1000,
			ScaleDownThreshold: 100,
		},
	}
}

func newTestObserver(t *testing.T) *PrometheusMetricsObserver {
	t.Helper()

	prometheusAddr := os.Getenv("PROMETHEUS_ADDR")
	if prometheusAddr == "" {
		t.Skip("PROMETHEUS_ADDR not set, skipping integration test")
	}

	querier, err := prometheusquery.NewPrometheusQuerier(prometheusAddr)
	if err != nil {
		t.Fatalf("create querier: %v", err)
	}

	return NewPrometheusMetricsObserver(querier)
}

func TestPrometheusMetricsObserver_Integration(t *testing.T) {
	observer := newTestObserver(t)
	specs := testSpecs()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	snapshot, err := observer.Observe(ctx, specs)
	if err != nil {
		t.Fatalf("Observe() failed: %v", err)
	}

	if snapshot.At.IsZero() {
		t.Error("snapshot.At not set")
	}

	for _, spec := range specs {
		val, ok := snapshot.Values[spec.Name]
		if !ok {
			t.Errorf("metric %q missing from snapshot", spec.Name)
			continue
		}
		t.Logf("  %s = %.4f", spec.Name, val)
	}
}

func TestPrometheusMetricsObserver_Timeout(t *testing.T) {
	observer := newTestObserver(t)
	specs := testSpecs()

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Millisecond)
	defer cancel()

	_, err := observer.Observe(ctx, specs)
	if err == nil {
		t.Log("Observe completed before timeout (fast network)")
	} else {
		t.Logf("Observe timed out as expected: %v", err)
	}
}

func TestPrometheusMetricsObserver_MultipleObserves(t *testing.T) {
	observer := newTestObserver(t)
	specs := testSpecs()
	ctx := context.Background()

	snap1, err := observer.Observe(ctx, specs)
	if err != nil {
		t.Fatalf("first Observe() failed: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	snap2, err := observer.Observe(ctx, specs)
	if err != nil {
		t.Fatalf("second Observe() failed: %v", err)
	}

	t.Logf("snapshot1 at=%v values=%v", snap1.At, snap1.Values)
	t.Logf("snapshot2 at=%v values=%v", snap2.At, snap2.Values)
	t.Logf("time delta=%v", snap2.At.Sub(snap1.At))
}
