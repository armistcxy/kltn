package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/armistcxy/loadgen/pkg/engine"
	"github.com/armistcxy/loadgen/pkg/metrics"
	"github.com/armistcxy/loadgen/pkg/pattern"
	"github.com/armistcxy/loadgen/pkg/workload"
)

var runFlags struct {
	DBURL             string
	DBURLs            string
	DiscoveryHost     string
	DiscoveryInterval time.Duration // how often to re-resolve DNS
	WorkloadName      string
	Concurrency       int
	Duration          time.Duration
	MaxRPS            float64
	ScenarioFile      string
	MetricsPort       int
	ScaleFactor       int
	PoolMaxConns      int // 0 = auto
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
	f.StringVar(&runFlags.DBURL, "db-url", "", "PostgreSQL connection URL")
	f.StringVar(&runFlags.DBURLs, "db-urls", "", "Comma-separated PostgreSQL URLs; splits --concurrency evenly across each target (bypasses kube-proxy for even distribution)")
	f.StringVar(&runFlags.DiscoveryHost, "discovery-host", "", "Headless service URL (e.g. postgres://user:pass@pg-cluster-headless:5432/app); resolves DNS to all pod IPs and distributes load evenly, re-resolving every --discovery-interval to track autoscaling")
	f.DurationVar(&runFlags.DiscoveryInterval, "discovery-interval", 10*time.Second, "How often to re-resolve --discovery-host DNS to detect new/removed pods")
	f.StringVar(&runFlags.WorkloadName, "workload", "pgbench-oltp", "Workload name (pgbench-oltp)")
	f.IntVar(&runFlags.Concurrency, "concurrency", 10, "Number of concurrent workers")
	f.DurationVar(&runFlags.Duration, "duration", 60*time.Second, "Test duration")
	f.Float64Var(&runFlags.MaxRPS, "max-rps", 0, "Global rate cap in requests/s (0 = unlimited)")
	f.StringVar(&runFlags.ScenarioFile, "scenario", "", "Optional YAML scenario file for stepped load patterns")
	f.IntVar(&runFlags.MetricsPort, "metrics-port", 9090, "Prometheus /metrics port (0 = disabled)")
	f.IntVar(&runFlags.ScaleFactor, "scale-factor", 1, "pgbench scale factor (matches -s used during pgbench -i)")
	f.IntVar(&runFlags.PoolMaxConns, "pool-max-conns", 0, "override pool MaxConns (default: concurrency+2); set equal to --concurrency for exactly one connection per worker")

	rootCmd.AddCommand(runCmd)
}

// scaleSetter is implemented by workloads that accept an explicit scale factor.
type scaleSetter interface{ SetScaleFactor(int) }

func runRun(_ *cobra.Command, _ []string) error {
	if runFlags.DBURL == "" && runFlags.DBURLs == "" && runFlags.DiscoveryHost == "" {
		return fmt.Errorf("one of --db-url, --db-urls, or --discovery-host is required")
	}

	wl, err := workload.Get(runFlags.WorkloadName)
	if err != nil {
		return err
	}

	if runFlags.ScaleFactor > 1 {
		if ss, ok := wl.(scaleSetter); ok {
			ss.SetScaleFactor(runFlags.ScaleFactor)
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if runFlags.MetricsPort > 0 {
		addr := fmt.Sprintf(":%d", runFlags.MetricsPort)
		go func() {
			if err := metrics.Server(ctx, addr); err != nil {
				fmt.Fprintf(os.Stderr, "metrics server error: %v\n", err)
			}
		}()
	}

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

	// Multi-target mode (distribute load evenly across explicit pod URLs)
	if runFlags.DBURLs != "" {
		return runMulti(ctx, wl, pat, duration, maxRPS)
	}

	// Discovery mode (resolve headless service DNS to per-pod pools, re-discover periodically)
	var discovery *engine.DiscoveryPool
	dbURL := runFlags.DBURL
	if runFlags.DiscoveryHost != "" {
		discovery = engine.NewDiscoveryPool(runFlags.DiscoveryHost, runFlags.Concurrency, runFlags.DiscoveryInterval)
		dbURL = ""
	}

	cfg := engine.Config{
		DBURL:        dbURL,
		Concurrency:  runFlags.Concurrency,
		Duration:     duration,
		MaxRPS:       maxRPS,
		Workload:     wl,
		ReportEvery:  5 * time.Second,
		OnSnapshot:   makeReporter(),
		Discovery:    discovery,
		PoolMaxConns: runFlags.PoolMaxConns,
	}

	eng := engine.New(cfg)

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

// runMulti spawns one engine per URL in --db-urls, splitting --concurrency evenly.
func runMulti(ctx context.Context, wl workload.Workload, pat *pattern.StepPattern, duration time.Duration, maxRPS float64) error {
	urls := strings.Split(runFlags.DBURLs, ",")
	n := len(urls)

	concPerEngine := runFlags.Concurrency / n
	if concPerEngine < 1 {
		concPerEngine = 1
	}
	rpsPerEngine := 0.0
	if maxRPS > 0 {
		rpsPerEngine = maxRPS / float64(n)
	}

	type latestSnap struct {
		mu  sync.Mutex
		val engine.Snapshot
	}
	snaps := make([]latestSnap, n)

	engines := make([]*engine.Engine, n)
	for i, rawURL := range urls {
		i := i
		cfg := engine.Config{
			DBURL:       strings.TrimSpace(rawURL),
			Concurrency: concPerEngine,
			Duration:    duration,
			MaxRPS:      rpsPerEngine,
			Workload:    wl,
			ReportEvery: 5 * time.Second,
			OnSnapshot: func(snap engine.Snapshot) {
				snaps[i].mu.Lock()
				snaps[i].val = snap
				snaps[i].mu.Unlock()
			},
		}
		engines[i] = engine.New(cfg)
	}

	reportCtx, cancelReport := context.WithCancel(ctx)
	defer cancelReport()
	go func() {
		// print combined TPS every 5s
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				var aggTPS float64
				var aggErrors, aggTotal int64
				var maxP95, maxElapsed time.Duration
				for i := range snaps {
					snaps[i].mu.Lock()
					v := snaps[i].val
					snaps[i].mu.Unlock()
					aggTPS += v.TPS
					aggErrors += v.Errors
					aggTotal += v.TotalOps
					if v.P95 > maxP95 {
						maxP95 = v.P95
					}
					if v.Elapsed > maxElapsed {
						maxElapsed = v.Elapsed
					}
				}
				fmt.Printf("[%5s] TPS(agg): %7.1f | P95(max): %7s | Errors: %d | Total: %d\n",
					maxElapsed.Round(time.Second), aggTPS, fmtDuration(maxP95), aggErrors, aggTotal)
			case <-reportCtx.Done():
				return
			}
		}
	}()

	printHeader(fmt.Sprintf("%s ×%d targets", runFlags.WorkloadName, n), concPerEngine*n, duration, maxRPS)

	var wg sync.WaitGroup
	results := make([]*engine.Summary, n)
	errs := make([]error, n)
	for i := range engines {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			if pat != nil {
				go drivePatternScaled(ctx, engines[i], pat, float64(n))
			}
			results[i], errs[i] = engines[i].Run(ctx)
		}()
	}
	wg.Wait()
	cancelReport()

	for i, e := range errs {
		if e != nil {
			return fmt.Errorf("engine[%d]: %w", i, e)
		}
	}

	printSummary(aggregateSummaries(results))
	return nil
}

// drivePatternScaled drives a single engine at pattern_rps/scaleFactor.
func drivePatternScaled(ctx context.Context, eng *engine.Engine, pat *pattern.StepPattern, scaleFactor float64) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	start := time.Now()

	initial := pat.RPS(0) / scaleFactor
	eng.SetRate(initial)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			rps := pat.RPS(time.Since(start)) / scaleFactor
			eng.SetRate(rps)
		}
	}
}

func aggregateSummaries(summaries []*engine.Summary) *engine.Summary {
	agg := &engine.Summary{}
	for _, s := range summaries {
		if s == nil {
			continue
		}
		agg.TotalOps += s.TotalOps
		agg.Errors += s.Errors
		agg.TPS += s.TPS
		if s.Duration > agg.Duration {
			agg.Duration = s.Duration
		}
		if s.P50Latency > agg.P50Latency {
			agg.P50Latency = s.P50Latency
		}
		if s.P95Latency > agg.P95Latency {
			agg.P95Latency = s.P95Latency
		}
		if s.P99Latency > agg.P99Latency {
			agg.P99Latency = s.P99Latency
		}
		if s.P999Latency > agg.P999Latency {
			agg.P999Latency = s.P999Latency
		}
	}
	return agg
}

// drivePattern adjusts the engine's rate every second according to the pattern.
func drivePattern(ctx context.Context, eng *engine.Engine, pat *pattern.StepPattern) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	start := time.Now()

	// apply the initial rate immediately so the engine doesn't fire unthrottled for the first second before the ticker fires
	initialRPS := pat.RPS(0)
	eng.SetRate(initialRPS)
	metrics.TargetRPS.Set(initialRPS)

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

func makeReporter() func(engine.Snapshot) {
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
