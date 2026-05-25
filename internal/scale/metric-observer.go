package scale

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	prometheusquery "github.com/armistcxy/kltn/pkg/prometheus-query"
)

type lastGoodEntry struct {
	value float64
	at    time.Time
}

// PrometheusMetricsObserver implements MetricsObserver by running PromQL queries
type PrometheusMetricsObserver struct {
	querier          *prometheusquery.PrometheusQuerier
	lastGoodValueTTL time.Duration

	mu             sync.Mutex
	lastGoodValues map[string]lastGoodEntry
}

func NewPrometheusMetricsObserver(querier *prometheusquery.PrometheusQuerier, lastGoodValueTTL time.Duration) *PrometheusMetricsObserver {
	return &PrometheusMetricsObserver{
		querier:          querier,
		lastGoodValueTTL: lastGoodValueTTL,
		lastGoodValues:   make(map[string]lastGoodEntry),
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
			entry, ok := o.lastGoodValues[name]
			if !ok || entry.value <= 0 {
				continue
			}
			age := time.Since(entry.at)
			if o.lastGoodValueTTL > 0 && age > o.lastGoodValueTTL {
				slog.Warn("metric returned 0, last good value expired",
					"metric", name,
					"lastGood", entry.value,
					"age", age.Round(time.Second),
					"ttl", o.lastGoodValueTTL,
				)
				continue
			}
			slog.Warn("metric returned 0, using last known good value",
				"metric", name,
				"lastGood", entry.value,
				"age", age.Round(time.Second),
			)
			snapshot.Values[name] = entry.value
		} else {
			o.lastGoodValues[name] = lastGoodEntry{value: value, at: time.Now()}
		}
	}
	o.mu.Unlock()

	return snapshot, nil
}
