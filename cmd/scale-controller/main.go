package main

import (
	"context"
	"flag"
	"log"
	"time"

	"github.com/armistcxy/kltn/internal/scale"
	prometheusquery "github.com/armistcxy/kltn/pkg/prometheus-query"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
)

func main() {
	// Min/max guardrails
	minInstances := flag.Int("min-instances", 1, "Minimum number of instances")
	maxInstances := flag.Int("max-instances", 10, "Maximum number of instances")

	// Backends thresholds
	backendsScaleUpThreshold := flag.Float64("backends-scale-up", 80, "Scale up when backends exceed this threshold (example: 80 connections)")
	backendsScaleDownThreshold := flag.Float64("backends-scale-down", 20, "Scale down when backends fall below this threshold (example: 20 connections)")

	// CPU thresholds
	cpuScaleUpThreshold := flag.Float64("cpu-scale-up", 1.2, "Scale up when CPU exceeds this threshold (example: 1.2 cores)")
	cpuScaleDownThreshold := flag.Float64("cpu-scale-down", 0.2, "Scale down when CPU falls below this threshold (example: 0.2 cores)")

	// Memory thresholds
	memoryScaleUpThreshold := flag.Float64("memory-scale-up", 1.5, "Scale up when memory exceeds this threshold (example: 1.5 GB)")
	memoryScaleDownThreshold := flag.Float64("memory-scale-down", 0.5, "Scale down when memory falls below this threshold (example: 0.5 GB)")

	// TPS thresholds
	tpsScaleUpThreshold := flag.Float64("tps-scale-up", 1000, "Scale up when TPS exceeds this threshold (example: 1000 tps)")
	tpsScaleDownThreshold := flag.Float64("tps-scale-down", 100, "Scale down when TPS falls below this threshold (example: 100 tps)")

	// Timers
	cooldown := flag.Duration("cooldown", 1*time.Minute, "Cooldown duration to prevent flapping")
	pollInterval := flag.Duration("poll-interval", 30*time.Second, "How often to poll for metrics")

	// Prometheus address
	prometheusAddr := flag.String("prometheus-addr", "http://localhost:9090", "Address of the Prometheus server")

	// Database cluster name and the deploy namespace
	dbCluster := flag.String("db-cluster", "pg-cluster", "Name of the PostgreSQL cluster to manage")
	namespace := flag.String("namespace", "default", "Kubernetes namespace where the PostgreSQL cluster is deployed")

	flag.Parse()

	// Validate flags
	if *minInstances < 1 {
		log.Fatal("min-instances must be at least 1")
	}
	if *maxInstances < *minInstances {
		log.Fatal("max-instances must be greater than or equal to min-instances")
	}

	scaleCtrlConfig := scale.Config{
		MinInstances:               *minInstances,
		MaxInstances:               *maxInstances,
		BackendsScaleUpThreshold:   *backendsScaleUpThreshold,
		BackendsScaleDownThreshold: *backendsScaleDownThreshold,
		CPUScaleUpThreshold:        *cpuScaleUpThreshold,
		CPUScaleDownThreshold:      *cpuScaleDownThreshold,
		MemoryScaleUpThreshold:     *memoryScaleUpThreshold,
		MemoryScaleDownThreshold:   *memoryScaleDownThreshold,
		TPSScaleUpThreshold:        *tpsScaleUpThreshold,
		TPSScaleDownThreshold:      *tpsScaleDownThreshold,
		Cooldown:                   *cooldown,
		PollInterval:               *pollInterval,
	}

	log.Printf("Scale Controller Config: %+v", scaleCtrlConfig)

	ptQuerier, err := prometheusquery.NewPrometheusQuerier(*prometheusAddr)
	if err != nil {
		log.Fatalf("Failed to initialize Prometheus querier: %v", err)
	}

	observer := scale.NewPrometheusMetricsObserver(ptQuerier, *namespace, *dbCluster, time.Minute)

	// Initialize Kubernetes client
	if err := cnpgv1.AddToScheme(scheme.Scheme); err != nil {
		log.Fatalf("Failed to add CNPG scheme: %v", err)
	}

	k8sConfig, err := config.GetConfig()
	if err != nil {
		log.Fatalf("Failed to load Kubernetes config: %v", err)
	}

	k8sClient, err := client.New(k8sConfig, client.Options{Scheme: scheme.Scheme})
	if err != nil {
		log.Fatalf("Failed to create Kubernetes client: %v", err)
	}

	cnpgClient := scale.NewCNPGClient(k8sClient, *namespace, *dbCluster)

	controller := scale.NewScaleController(scaleCtrlConfig, observer, cnpgClient)
	if err := controller.Run(context.Background()); err != nil {
		log.Fatal(err)
	}
}
