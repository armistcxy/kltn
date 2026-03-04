package main

import (
	"context"
	"flag"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/armistcxy/kltn/internal/scale"
	prometheusquery "github.com/armistcxy/kltn/pkg/prometheus-query"
	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
)

func main() {
	configPath := flag.String("config", "config.yaml", "Path to YAML config file")
	prometheusAddr := flag.String("prometheus-addr", "http://localhost:9090", "Prometheus server address")
	namespace := flag.String("namespace", "default", "Kubernetes namespace of the CNPG cluster")
	dbCluster := flag.String("db-cluster", "pg-cluster", "Name of the CNPG cluster to manage")
	watchInterval := flag.Duration("watch-interval", 10*time.Second, "How often to check the config file for changes")
	flag.Parse()

	// Structured logging.
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})))

	// Load initial config.
	cfg, err := scale.LoadConfig(*configPath)
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}
	slog.Info("config loaded", "path", *configPath, "metrics", len(cfg.Metrics))

	// Prometheus querier.
	querier, err := prometheusquery.NewPrometheusQuerier(*prometheusAddr)
	if err != nil {
		log.Fatalf("failed to create Prometheus querier: %v", err)
	}

	observer := scale.NewPrometheusMetricsObserver(querier)

	// Kubernetes client with CNPG scheme.
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

	// Build controller.
	controller := scale.NewScaleController(cfg, observer, cnpgClient)

	// Optional: attach a predictor here.
	// Uncomment and replace with a real algorithm once implemented:
	//
	//   controller.WithPredictor(scale.NewMovingAveragePredictor(10))
	//
	// Or use PredictorFunc for a custom inline algorithm:
	//
	//   controller.WithPredictor(scale.NewPredictorFunc("my_algo",
	//       func(ctx context.Context, history []scale.DataPoint, horizon time.Duration) (float64, error) {
	//           // your algorithm
	//           return forecast, nil
	//       },
	//   ))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Watch config file and hot-reload on change.
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
