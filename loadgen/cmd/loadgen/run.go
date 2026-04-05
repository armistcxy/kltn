package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/armistcxy/loadgen/pkg/engine"
	"github.com/armistcxy/loadgen/pkg/metrics"
	"github.com/armistcxy/loadgen/pkg/pattern"
	"github.com/armistcxy/loadgen/pkg/workload"
)

var runFlags struct {
	DBURL        string
	WorkloadName string
	Concurrency  int
	Duration     time.Duration
	MaxRPS       float64
	ScenarioFile string
	MetricsPort  int
	ScaleFactor  int
}

var runCmd = &cobra.Command{
	Use:   "run",
	Short: "Run a load test",
	Example: `  loadgen run \
    --db-url "postgres://user:pass@localhost:5432/bench?sslmode=disable" \
    --workload pgbench-oltp \
    --concurrency 50 \
    --duration 300s \
    --metrics-port 9090`,
	RunE: runRun,
}

func init() {
	f := runCmd.Flags()
	f.StringVar(&runFlags.DBURL, "db-url", "", "PostgreSQL connection URL (required)")
	f.StringVar(&runFlags.WorkloadName, "workload", "pgbench-oltp", "Workload name (pgbench-oltp)")
	f.IntVar(&runFlags.Concurrency, "concurrency", 10, "Number of concurrent workers")
	f.DurationVar(&runFlags.Duration, "duration", 60*time.Second, "Test duration")
	f.Float64Var(&runFlags.MaxRPS, "max-rps", 0, "Global rate cap in requests/s (0 = unlimited)")
	f.StringVar(&runFlags.ScenarioFile, "scenario", "", "Optional YAML scenario file for stepped load patterns")
	f.IntVar(&runFlags.MetricsPort, "metrics-port", 9090, "Prometheus /metrics port (0 = disabled)")
	f.IntVar(&runFlags.ScaleFactor, "scale-factor", 1, "pgbench scale factor (matches -s used during pgbench -i)")

	runCmd.MarkFlagRequired("db-url")
	rootCmd.AddCommand(runCmd)
}

// scaleSetter is implemented by workloads that accept an explicit scale factor.
type scaleSetter interface{ SetScaleFactor(int) }

func runRun(_ *cobra.Command, _ []string) error {
	wl, err := workload.Get(runFlags.WorkloadName)
	if err != nil {
		return err
	}

	// Wire --scale-factor to the workload. If > 1 it also suppresses auto-detection
	// in Prepare(), so the user's explicit value is respected.
	if runFlags.ScaleFactor > 1 {
		if ss, ok := wl.(scaleSetter); ok {
			ss.SetScaleFactor(runFlags.ScaleFactor)
		}
	}

	// Handle graceful shutdown on SIGINT/SIGTERM.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Start metrics server if requested.
	if runFlags.MetricsPort > 0 {
		addr := fmt.Sprintf(":%d", runFlags.MetricsPort)
		go func() {
			if err := metrics.Server(ctx, addr); err != nil {
				fmt.Fprintf(os.Stderr, "metrics server error: %v\n", err)
			}
		}()
	}

	// Determine duration from scenario file if provided.
	duration := runFlags.Duration
	maxRPS := runFlags.MaxRPS
	var pat *pattern.StepPattern
	if runFlags.ScenarioFile != "" {
		pat, err = pattern.LoadFile(runFlags.ScenarioFile)
		if err != nil {
			return err
		}
		if d := pat.TotalDuration(); d > 0 {
			duration = d
		}
	}

	cfg := engine.Config{
		DBURL:       runFlags.DBURL,
		Concurrency: runFlags.Concurrency,
		Duration:    duration,
		MaxRPS:      maxRPS,
		Workload:    wl,
		ReportEvery: 5 * time.Second,
		OnSnapshot:  makeReporter(wl.Name()),
	}

	eng := engine.New(cfg)

	// If a scenario was loaded, drive the rate limiter according to the pattern.
	if pat != nil {
		go drivePattern(ctx, eng, pat)
	}

	printHeader(runFlags.WorkloadName, runFlags.Concurrency, duration, maxRPS)

	sum, err := eng.Run(ctx)
	if err != nil {
		return fmt.Errorf("engine: %w", err)
	}

	printSummary(sum)
	return nil
}

// drivePattern adjusts the engine's rate every second according to the pattern.
// It also updates metrics.TargetRPS so the autoscaler can use the intended load
// as a scaling signal — avoiding connection pool pre-allocation noise from
// pg_stat_activity.
func drivePattern(ctx context.Context, eng *engine.Engine, pat *pattern.StepPattern) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	start := time.Now()
	for {
		select {
		case <-ctx.Done():
			metrics.TargetRPS.Set(0)
			return
		case <-ticker.C:
			elapsed := time.Since(start)
			rps := pat.RPS(elapsed)
			eng.SetRate(rps)
			metrics.TargetRPS.Set(rps)
		}
	}
}

func makeReporter(workloadName string) func(engine.Snapshot) {
	return func(snap engine.Snapshot) {
		// Update Prometheus gauges.
		metrics.CurrentRPS.Set(snap.TPS)

		// Print to console.
		fmt.Println(engine.FormatSnapshot(snap))
	}
}

func printHeader(wl string, concurrency int, duration time.Duration, maxRPS float64) {
	rpsStr := "unlimited"
	if maxRPS > 0 {
		rpsStr = fmt.Sprintf("%.0f rps", maxRPS)
	}
	fmt.Printf("╔═══════════════════════════════════════════════════╗\n")
	fmt.Printf("║  loadgen — PostgreSQL load generator              ║\n")
	fmt.Printf("╠═══════════════════════════════════════════════════╣\n")
	fmt.Printf("║  workload:    %-36s║\n", wl)
	fmt.Printf("║  concurrency: %-36d║\n", concurrency)
	fmt.Printf("║  duration:    %-36s║\n", duration)
	fmt.Printf("║  rate cap:    %-36s║\n", rpsStr)
	fmt.Printf("╚═══════════════════════════════════════════════════╝\n")
	fmt.Println()
}

func printSummary(sum *engine.Summary) {
	fmt.Println()
	fmt.Printf("╔═══════════════════════════════════════╗\n")
	fmt.Printf("║           Final Summary               ║\n")
	fmt.Printf("╠═══════════════════════════════════════╣\n")
	fmt.Printf("║  Duration:      %-22s║\n", sum.Duration.Round(time.Millisecond))
	fmt.Printf("║  Total Ops:     %-22d║\n", sum.TotalOps)
	fmt.Printf("║  Errors:        %-22d║\n", sum.Errors)
	fmt.Printf("║  TPS:           %-22s║\n", fmt.Sprintf("%.2f", sum.TPS))
	fmt.Printf("║  P50 Latency:   %-22s║\n", fmtDuration(sum.P50Latency))
	fmt.Printf("║  P95 Latency:   %-22s║\n", fmtDuration(sum.P95Latency))
	fmt.Printf("║  P99 Latency:   %-22s║\n", fmtDuration(sum.P99Latency))
	fmt.Printf("║  P99.9 Latency: %-22s║\n", fmtDuration(sum.P999Latency))
	fmt.Printf("╚═══════════════════════════════════════╝\n")
}

func fmtDuration(d time.Duration) string {
	switch {
	case d >= time.Second:
		return fmt.Sprintf("%.3fs", d.Seconds())
	case d >= time.Millisecond:
		return fmt.Sprintf("%.3fms", float64(d)/float64(time.Millisecond))
	default:
		return fmt.Sprintf("%.0fµs", float64(d)/float64(time.Microsecond))
	}
}
