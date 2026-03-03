package prometheusquery

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/prometheus/common/model"
)

// MetricSample represents a single sample of a metric, including its value and labels.
type MetricSample struct {
	Labels    map[string]string
	Value     float64
	Timestamp time.Time
}

// GetCustomMetric queries any custom metric with given PromQL query.
func (q *PrometheusQuerier) GetCustomMetric(
	ctx context.Context,
	query string,
) ([]MetricSample, error) {
	result, warnings, err := q.Query(ctx, query, time.Now())
	if err != nil {
		return nil, fmt.Errorf("prometheus query failed: %w", err)
	}

	if len(warnings) > 0 {
		slog.Warn("prometheus warnings", "warnings", warnings)
	}

	switch v := result.(type) {

	case model.Vector:
		out := make([]MetricSample, 0, len(v))
		for _, sample := range v {
			labels := make(map[string]string, len(sample.Metric))
			for k, val := range sample.Metric {
				labels[string(k)] = string(val)
			}

			out = append(out, MetricSample{
				Labels:    labels,
				Value:     float64(sample.Value),
				Timestamp: sample.Timestamp.Time(),
			})
		}
		return out, nil
	case *model.Scalar:
		return []MetricSample{
			{
				Labels:    nil,
				Value:     float64(v.Value),
				Timestamp: v.Timestamp.Time(),
			},
		}, nil
	default:
		return nil, fmt.Errorf("unsupported result type: %T", result)
	}
}
