package main

import (
	"context"
	"flag"
	"io"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/armistcxy/kltn/internal/scale"
	prometheusquery "github.com/armistcxy/kltn/pkg/prometheus-query"
	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	"github.com/go-logr/logr"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"
)

func main() {
	configPath := flag.String("config", "config.yaml", "Path to YAML config file")
	prometheusAddr := flag.String("prometheus-addr", "http://localhost:9090", "Prometheus server address")
	namespace := flag.String("namespace", "default", "Kubernetes namespace of the CNPG cluster")
	dbCluster := flag.String("db-cluster", "pg-cluster", "Name of the CNPG cluster to manage")
	watchInterval := flag.Duration("watch-interval", 10*time.Second, "How often to check the config file for changes")
	metricsAddr := flag.String("metrics-addr", ":9091", "Address to expose controller Prometheus metrics on")
	logFile := flag.String("log-file", "scale-controller.log", "Path to log file (written alongside stdout)")
	flag.Parse()

	// Write log to a file and stdout simultaneously
	f, err := os.OpenFile(*logFile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		log.Fatalf("failed to open log file %s: %v", *logFile, err)
	}
	defer f.Close()
	logWriter := io.MultiWriter(os.Stdout, f)
	handler := slog.NewTextHandler(logWriter, &slog.HandlerOptions{Level: slog.LevelInfo})
	slog.SetDefault(slog.New(handler))
	ctrllog.SetLogger(logr.FromSlogHandler(handler))

	// Load initial config from yaml file
	cfg, err := scale.LoadConfig(*configPath)
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}
	slog.Info("config loaded", "path", *configPath, "metrics", len(cfg.Metrics))

	// Init Prometheus querier for querying metrics used for scaling decisions
	querier, err := prometheusquery.NewPrometheusQuerier(*prometheusAddr)
	if err != nil {
		log.Fatalf("failed to create Prometheus querier: %v", err)
	}
	observer := scale.NewPrometheusMetricsObserver(querier)

	// Kubernetes client with CNPG scheme registered, used for managing CNPG cluster and watching for changes
	if err := cnpgv1.AddToScheme(scheme.Scheme); err != nil {
		log.Fatalf("failed to register CNPG scheme: %v", err)
	}
	k8sCfg, err := config.GetConfig()
	if err != nil {
		log.Fatalf("failed to load kubeconfig: %v", err)
	}
	k8sClient, err := client.New(k8sCfg, client.Options{Scheme: scheme.Scheme})
	if err != nil {
		log.Fatalf("failed to create Kubernetes client: %v", err)
	}

	cnpgClient := scale.NewCNPGClient(k8sClient, *namespace, *dbCluster)

	controller := scale.NewScaleController(cfg, observer, cnpgClient)

	cm := scale.NewControllerMetrics(prometheus.DefaultRegisterer)
	controller.WithMetrics(cm)
	go func() {
		// export controller metrics to monitor itself
		mux := http.NewServeMux()
		mux.Handle("/metrics", promhttp.Handler())
		slog.Info("metrics server listening", "addr", *metricsAddr)
		if err := http.ListenAndServe(*metricsAddr, mux); err != nil {
			log.Printf("metrics server error: %v", err)
		}
	}()

	if predictor, err := scale.BuildPredictor(cfg.Prediction); err != nil {
		log.Fatalf("failed to build predictor: %v", err)
	} else if predictor != nil {
		controller.WithPredictor(predictor)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go scale.WatchConfig(ctx, *configPath, *watchInterval, func(newCfg scale.Config) {
		controller.UpdateConfig(newCfg)
	})

	slog.Info("starting scale controller",
		"cluster", *dbCluster,
		"namespace", *namespace,
		"prometheus", *prometheusAddr,
	)

	if err := controller.Run(ctx); err != nil && err != context.Canceled {
		log.Fatalf("controller stopped: %v", err)
	}
}
