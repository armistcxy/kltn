package engine

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/time/rate"
)

// Workload is executed repeatedly by each worker goroutine.
// Each worker holds a single persistent *pgxpool.Conn for its lifetime —
// matching pgbench's one-connection-per-client model and eliminating
// per-transaction Acquire/Release overhead.
type Workload interface {
	Name() string
	// Execute runs one unit of work on the provided persistent connection.
	Execute(ctx context.Context, conn *pgxpool.Conn) error
}

// Preparer is an optional interface a Workload can implement to perform
// one-time setup (e.g. querying DB metadata) before workers are spawned.
type Preparer interface {
	Prepare(ctx context.Context, pool *pgxpool.Pool) error
}

// Config holds all engine parameters.
type Config struct {
	DBURL       string
	Concurrency int
	Duration    time.Duration
	// MaxRPS sets a global request rate cap. 0 means unlimited.
	MaxRPS      float64
	Workload    Workload
	ReportEvery time.Duration
	// OnSnapshot is called after each reporting interval with the current stats.
	// If nil, FormatSnapshot output is printed to stdout.
	OnSnapshot func(Snapshot)
}

// Engine manages the load generation loop.
type Engine struct {
	config  Config
	limiter *rate.Limiter
	stats   *Stats
	// pool may be injected externally (e.g. for testing); otherwise created in Run.
	pool *pgxpool.Pool
}

func New(config Config) *Engine {
	if config.ReportEvery == 0 {
		config.ReportEvery = 5 * time.Second
	}

	var lim *rate.Limiter
	if config.MaxRPS > 0 {
		burst := int(config.MaxRPS)
		if burst < 1 {
			burst = 1
		}
		lim = rate.NewLimiter(rate.Limit(config.MaxRPS), burst)
	} else {
		lim = rate.NewLimiter(rate.Inf, 0)
	}

	return &Engine{
		config:  config,
		limiter: lim,
		stats:   NewStats(),
	}
}

// SetRate adjusts the global rate cap at runtime (thread-safe).
func (e *Engine) SetRate(rps float64) {
	if rps <= 0 {
		e.limiter.SetLimit(rate.Inf)
		e.limiter.SetBurst(0)
	} else {
		e.limiter.SetLimit(rate.Limit(rps))
		burst := int(rps)
		if burst < 1 {
			burst = 1
		}
		e.limiter.SetBurst(burst)
	}
}

// SetPool injects a pre-built pool (used in tests to avoid a real DB).
func (e *Engine) SetPool(pool *pgxpool.Pool) {
	e.pool = pool
}

// Run connects to the database, spawns workers, and drives load until the
// context is cancelled or the configured Duration elapses.
func (e *Engine) Run(ctx context.Context) (*Summary, error) {
	if e.pool == nil {
		pool, err := e.connect(ctx)
		if err != nil {
			return nil, fmt.Errorf("connect: %w", err)
		}
		defer pool.Close()
		e.pool = pool
	}
	return e.run(ctx, e.pool)
}

func (e *Engine) connect(ctx context.Context) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(e.config.DBURL)
	if err != nil {
		return nil, err
	}
	cfg.MaxConns = int32(e.config.Concurrency + 2)
	cfg.MinConns = int32(e.config.Concurrency)
	return pgxpool.NewWithConfig(ctx, cfg)
}

// run is the internal execution loop. It always applies the configured Duration
// as a hard deadline on top of ctx, so tests can call it directly with any context.
func (e *Engine) run(ctx context.Context, pool *pgxpool.Pool) (*Summary, error) {
	runCtx, cancel := context.WithTimeout(ctx, e.config.Duration)
	defer cancel()

	// One-time workload setup (e.g. scale factor detection from DB).
	if p, ok := e.config.Workload.(Preparer); ok && pool != nil {
		if err := p.Prepare(runCtx, pool); err != nil {
			return nil, fmt.Errorf("workload prepare: %w", err)
		}
	}

	e.stats = NewStats()
	e.stats.Start()

	var wg sync.WaitGroup
	for range e.config.Concurrency {
		wg.Add(1)
		go func() {
			defer wg.Done()
			e.workerLoop(runCtx, pool)
		}()
	}

	// Periodic reporter runs until runCtx is done.
	reportDone := make(chan struct{})
	go func() {
		defer close(reportDone)
		ticker := time.NewTicker(e.config.ReportEvery)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				snap := e.stats.Snapshot()
				e.report(snap)
			case <-runCtx.Done():
				return
			}
		}
	}()

	wg.Wait()
	<-reportDone

	return e.stats.FinalSummary(), nil
}

// workerLoop acquires one connection and holds it for the worker's entire
// lifetime, executing transactions back-to-back without Acquire/Release
// overhead — identical to pgbench's client model.
func (e *Engine) workerLoop(ctx context.Context, pool *pgxpool.Pool) {
	var conn *pgxpool.Conn

	if pool != nil {
		var err error
		conn, err = pool.Acquire(ctx)
		if err != nil {
			return // context already cancelled during startup
		}
		defer conn.Release()
	}

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		if err := e.limiter.Wait(ctx); err != nil {
			return
		}

		start := time.Now()
		execErr := e.config.Workload.Execute(ctx, conn)
		e.stats.Record(time.Since(start), execErr)
	}
}

func (e *Engine) report(snap Snapshot) {
	if e.config.OnSnapshot != nil {
		e.config.OnSnapshot(snap)
	} else {
		fmt.Println(FormatSnapshot(snap))
	}
}
