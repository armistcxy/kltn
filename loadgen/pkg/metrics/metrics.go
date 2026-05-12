package metrics

import (
	"context"
	"fmt"
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	OpsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "loadgen",
		Name:      "ops_total",
		Help:      "Total number of operations executed.",
	}, []string{"workload", "status"})

	LatencyHistogram = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "loadgen",
		Name:      "latency_seconds",
		Help:      "Operation latency distribution.",
		Buckets:   prometheus.ExponentialBucketsRange(0.0001, 10, 30),
	}, []string{"workload"})

	ActiveWorkers = promauto.NewGauge(prometheus.GaugeOpts{
		Namespace: "loadgen",
		Name:      "active_workers",
		Help:      "Number of currently active worker goroutines.",
	})

	CurrentRPS = promauto.NewGauge(prometheus.GaugeOpts{
		Namespace: "loadgen",
		Name:      "current_rps",
		Help:      "Observed requests per second over the last reporting interval.",
	})

	TargetRPS = promauto.NewGauge(prometheus.GaugeOpts{
		Namespace: "loadgen",
		Name:      "target_rps",
		Help:      "Target requests per second as defined by the current scenario step.",
	})
)

// Server starts a Prometheus metrics HTTP server on the given address
func Server(ctx context.Context, addr string) error {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintln(w, "ok")
	})

	srv := &http.Server{Addr: addr, Handler: mux}

	go func() {
		<-ctx.Done()
		srv.Shutdown(context.Background()) //nolint:errcheck
	}()

	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("metrics server: %w", err)
	}
	return nil
}
