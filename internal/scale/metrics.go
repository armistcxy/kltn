package scale

import "github.com/prometheus/client_golang/prometheus"

// ControllerMetrics holds the Prometheus metrics exported by the ScaleController
type ControllerMetrics struct {
	observerRawValue *prometheus.GaugeVec
	observerAvgValue *prometheus.GaugeVec

	instancesCurrent          prometheus.Gauge
	instancesTargetReactive   prometheus.Gauge
	instancesTargetPredictive prometheus.Gauge
	instancesTargetFinal      prometheus.Gauge
}

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

		instancesCurrent: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "scaling_instances_current",
			Help: "Current number of replicas in the CNPG cluster.",
		}),

		instancesTargetReactive: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "scaling_instances_target_reactive",
			Help: "Desired replica count computed from current load (reactive logic only, before predictive).",
		}),

		instancesTargetPredictive: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "scaling_instances_target_predictive",
			Help: "Desired replica count computed by the Predictor based on forecasted future load.",
		}),

		instancesTargetFinal: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "scaling_instances_target_final",
			Help: "Final desired replica count after Max(reactive, predictive) clamped to [minInstances, maxInstances].",
		}),
	}
	reg.MustRegister(
		m.observerRawValue,
		m.observerAvgValue,
		m.instancesCurrent,
		m.instancesTargetReactive,
		m.instancesTargetPredictive,
		m.instancesTargetFinal,
	)
	return m
}

func (m *ControllerMetrics) recordRaw(metricName string, value float64) {
	m.observerRawValue.WithLabelValues(metricName).Set(value)
}

func (m *ControllerMetrics) recordAvg(metricName string, avg float64) {
	m.observerAvgValue.WithLabelValues(metricName).Set(avg)
}

func (m *ControllerMetrics) recordDecision(current, reactive, predictive, final int) {
	m.instancesCurrent.Set(float64(current))
	m.instancesTargetReactive.Set(float64(reactive))
	m.instancesTargetPredictive.Set(float64(predictive))
	m.instancesTargetFinal.Set(float64(final))
}
