package scale

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	prometheusquery "github.com/armistcxy/kltn/pkg/prometheus-query"
)

// PrometheusMetricsObserver implements MetricsObserver by running PromQL queries
type PrometheusMetricsObserver struct {
	querier *prometheusquery.PrometheusQuerier

	mu             sync.Mutex
	lastGoodValues map[string]float64
}

func NewPrometheusMetricsObserver(querier *prometheusquery.PrometheusQuerier) *PrometheusMetricsObserver {
	return &PrometheusMetricsObserver{
		querier:        querier,
		lastGoodValues: make(map[string]float64),
	}
}

// Observe queries every MetricSpec in parallel and returns a snapshot
func (o *PrometheusMetricsObserver) Observe(ctx context.Context, specs []MetricSpec) (*MetricsSnapshot, error) {
	type result struct {
		name  string
		value float64
		err   error
	}

	ch := make(chan result, len(specs))

	for _, spec := range specs {
		spec := spec
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
