package scale

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	prometheusquery "github.com/armistcxy/kltn/pkg/prometheus-query"
)

// PrometheusMetricsObserver implements MetricsObserver by running arbitrary PromQL queries.
// The PromQL queries are fully specified in each MetricSpec.Query, so this observer
// is decoupled from any particular set of metrics.
//
// Stale-value protection: if a freshly-queried value is 0 but the previous known
// good value (> 0) exists, the previous value is kept and a warning is logged.
// This prevents spurious scale-downs caused by rate() windows not yet aligning
// with the Prometheus scrape interval.
type PrometheusMetricsObserver struct {
	querier *prometheusquery.PrometheusQuerier

	mu             sync.Mutex
	lastGoodValues map[string]float64
}

// NewPrometheusMetricsObserver creates a new observer backed by Prometheus.
func NewPrometheusMetricsObserver(querier *prometheusquery.PrometheusQuerier) *PrometheusMetricsObserver {
	return &PrometheusMetricsObserver{
		querier:        querier,
		lastGoodValues: make(map[string]float64),
	}
}

// Observe queries every MetricSpec in parallel and returns a snapshot.
// Returns an error if any metric query fails.
func (o *PrometheusMetricsObserver) Observe(ctx context.Context, specs []MetricSpec) (*MetricsSnapshot, error) {
	type result struct {
		name  string
		value float64
		err   error
	}

	ch := make(chan result, len(specs))

	for _, spec := range specs {
		spec := spec // capture
		go func() {
			val, err := o.querier.QueryScalar(ctx, spec.Query)
			ch <- result{name: spec.Name, value: val, err: err}
		}()
	}

	snapshot := &MetricsSnapshot{
		At:     time.Now(),
		Values: make(map[string]float64, len(specs)),
	}

	var failed []string
	for range specs {
		r := <-ch
		if r.err != nil {
			failed = append(failed, fmt.Sprintf("%s: %v", r.name, r.err))
		} else {
			snapshot.Values[r.name] = r.value
		}
	}

	if len(failed) > 0 {
		return nil, fmt.Errorf("metric query failures: %v", failed)
	}

	// Stale-value protection: replace zero values with the last known good value.
	o.mu.Lock()
	for name, value := range snapshot.Values {
		if value == 0 {
			if last, ok := o.lastGoodValues[name]; ok && last > 0 {
				slog.Warn("metric returned 0, using last known good value",
					"metric", name,
					"lastGood", last,
				)
				snapshot.Values[name] = last
			}
		} else {
			o.lastGoodValues[name] = value
		}
	}
	o.mu.Unlock()

	return snapshot, nil
}
