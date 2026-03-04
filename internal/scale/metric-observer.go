package scale

import (
	"context"
	"fmt"
	"time"

	prometheusquery "github.com/armistcxy/kltn/pkg/prometheus-query"
)

// PrometheusMetricsObserver implements MetricsObserver by running arbitrary PromQL queries.
// The PromQL queries are fully specified in each MetricSpec.Query, so this observer
// is decoupled from any particular set of metrics.
type PrometheusMetricsObserver struct {
	querier *prometheusquery.PrometheusQuerier
}

// NewPrometheusMetricsObserver creates a new observer backed by Prometheus.
func NewPrometheusMetricsObserver(querier *prometheusquery.PrometheusQuerier) *PrometheusMetricsObserver {
	return &PrometheusMetricsObserver{querier: querier}
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

	return snapshot, nil
}
