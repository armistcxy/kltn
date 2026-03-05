package scale

import "github.com/prometheus/client_golang/prometheus"

// ControllerMetrics holds the Prometheus metrics exported by the ScaleController.
// Register Group 1: Observer Input Metrics.
type ControllerMetrics struct {
	// observerRawValue is the instantaneous value fetched from Prometheus on the last poll.
	observerRawValue *prometheus.GaugeVec

	// observerAvgValue is the moving average of the metric across the full rolling history buffer.
	// This line will appear smoother than observerRawValue in Grafana, demonstrating noise filtering.
	observerAvgValue *prometheus.GaugeVec
}

// NewControllerMetrics creates and registers all controller metrics against reg.
// Pass prometheus.DefaultRegisterer for the standard global registry.
func NewControllerMetrics(reg prometheus.Registerer) *ControllerMetrics {
	m := &ControllerMetrics{
		observerRawValue: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "scaling_observer_metric_value",
			Help: "Instantaneous metric value fetched by the Observer on the last poll.",
		}, []string{"metric_name"}),

		observerAvgValue: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "scaling_observer_metric_avg",
			Help: "Moving average of the metric value across the controller's full rolling history buffer.",
		}, []string{"metric_name"}),
	}
	reg.MustRegister(m.observerRawValue, m.observerAvgValue)
	return m
}

func (m *ControllerMetrics) recordRaw(metricName string, value float64) {
	m.observerRawValue.WithLabelValues(metricName).Set(value)
}

func (m *ControllerMetrics) recordAvg(metricName string, avg float64) {
	m.observerAvgValue.WithLabelValues(metricName).Set(avg)
}
