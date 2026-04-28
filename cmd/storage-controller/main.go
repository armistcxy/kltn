package main

import (
	"context"
	"flag"
	"io"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	prometheusquery "github.com/armistcxy/kltn/pkg/prometheus-query"
	"github.com/armistcxy/kltn/pkg/storage"
	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	"github.com/go-logr/logr"
	"k8s.io/client-go/kubernetes/scheme"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"
)

func main() {
	configPath := flag.String("config", "config.storage-example.yaml", "Path to storage controller YAML config file")
	prometheusAddr := flag.String("prometheus-addr", "http://localhost:9090", "Prometheus server address")
	logFile := flag.String("log-file", "storage-controller.log", "Path to log file (written alongside stdout)")
	flag.Parse()

	// Structured logging — tee to stdout and a file.
	f, err := os.OpenFile(*logFile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		log.Fatalf("failed to open log file %s: %v", *logFile, err)
	}
	defer f.Close()
	logWriter := io.MultiWriter(os.Stdout, f)
	handler := slog.NewTextHandler(logWriter, &slog.HandlerOptions{Level: slog.LevelInfo})
	slog.SetDefault(slog.New(handler))
	ctrllog.SetLogger(logr.FromSlogHandler(handler))

	// Load config.
	cfg, err := storage.LoadConfig(*configPath)
	if err != nil {
		log.Fatalf("failed to load storage config: %v", err)
	}
	if !cfg.Enabled {
		slog.Info("storage scaling is disabled in config — exiting")
		return
	}
	slog.Info("storage config loaded",
		"namespace", cfg.Namespace,
		"cluster", cfg.Cluster,
		"pollInterval", cfg.PollInterval,
	)

	// Prometheus querier.
	querier, err := prometheusquery.NewPrometheusQuerier(*prometheusAddr)
	if err != nil {
		log.Fatalf("failed to create Prometheus querier: %v", err)
	}

	// Kubernetes client with CNPG scheme.
	if err := cnpgv1.AddToScheme(scheme.Scheme); err != nil {
		log.Fatalf("failed to register CNPG scheme: %v", err)
	}
	k8sCfg, err := config.GetConfig()
	if err != nil {
		log.Fatalf("failed to load kubeconfig: %v", err)
	}
	k8sClient, err := ctrlclient.New(k8sCfg, ctrlclient.Options{Scheme: scheme.Scheme})
	if err != nil {
		log.Fatalf("failed to create Kubernetes client: %v", err)
	}

	// Wire components.
	observer := storage.NewObserver(querier, k8sClient)
	decider := storage.NewDecider()
	actor := storage.NewActor(k8sClient, cfg.Namespace, cfg.Cluster)
	controller := storage.NewController(cfg, observer, decider, actor)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	slog.Info("starting storage controller",
		"cluster", cfg.Cluster,
		"namespace", cfg.Namespace,
		"prometheus", *prometheusAddr,
	)

	if err := controller.Run(ctx); err != nil && err != context.Canceled {
		log.Fatalf("storage controller stopped: %v", err)
	}
}
