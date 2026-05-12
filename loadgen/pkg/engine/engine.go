package engine

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/time/rate"
)

type connPool interface {
	Acquire(context.Context) (*pgxpool.Conn, error)
}

// Workload is executed repeatedly by each worker goroutine
type Workload interface {
	Name() string
	Execute(ctx context.Context, conn *pgxpool.Conn) error
}

// Preparer is an optional interface a Workload can implement to perform setup before workers are spawned
type Preparer interface {
	Prepare(ctx context.Context, pool *pgxpool.Pool) error
}

// Config holds all engine parameters
type Config struct {
	DBURL       string
	Concurrency int
	Duration    time.Duration
	// MaxRPS sets a global request rate cap (0 means unlimited)
	MaxRPS      float64
	Workload    Workload
	ReportEvery time.Duration
	// OnSnapshot is called after each reporting interval with the current stats (default: print to stdout)
	OnSnapshot   func(Snapshot)
	Discovery    *DiscoveryPool
	PoolMaxConns int
}

// Engine manages the load generation loop
type Engine struct {
	config  Config
	limiter *rate.Limiter
	stats   *Stats
	pool    *pgxpool.Pool
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

// SetRate adjusts the global rate cap at runtime
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

// SetPool injects a pre-built pool (used in tests to avoid a real DB)
func (e *Engine) SetPool(pool *pgxpool.Pool) {
	e.pool = pool
}

// Run connects to the database, spawns workers, and drives load
func (e *Engine) Run(ctx context.Context) (*Summary, error) {
	if e.config.Discovery != nil {
		dp := e.config.Discovery
		if err := dp.Start(ctx); err != nil {
			return nil, fmt.Errorf("discovery: %w", err)
		}
		defer dp.Close()
		if p, ok := e.config.Workload.(Preparer); ok {
			if pool := dp.AnyPool(); pool != nil {
				if err := p.Prepare(ctx, pool); err != nil {
					return nil, fmt.Errorf("workload prepare: %w", err)
				}
			}
		}
		return e.run(ctx, dp)
	}

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
	maxConns := int32(e.config.Concurrency + 2)
	if e.config.PoolMaxConns > 0 {
		maxConns = int32(e.config.PoolMaxConns)
	}
	cfg.MaxConns = maxConns
	cfg.MinConns = 0
	cfg.MaxConnLifetime = 30 * time.Second
	cfg.MaxConnIdleTime = 10 * time.Second
	return pgxpool.NewWithConfig(ctx, cfg)
}

func (e *Engine) run(ctx context.Context, cp connPool) (*Summary, error) {
	runCtx, cancel := context.WithTimeout(ctx, e.config.Duration)
	defer cancel()

	if pool, ok := cp.(*pgxpool.Pool); ok {
		if p, ok := e.config.Workload.(Preparer); ok && pool != nil {
			if err := p.Prepare(runCtx, pool); err != nil {
				return nil, fmt.Errorf("workload prepare: %w", err)
			}
		}
	}

	if cp != nil {
		// Pre-warm the connection pool before starting the stats clock
		warmup := make([]*pgxpool.Conn, e.config.Concurrency)
		for i := range warmup {
			var err error
			if dp, ok := cp.(*DiscoveryPool); ok {
				warmup[i], err = dp.AcquireForWorker(runCtx, i)
			} else {
				warmup[i], err = cp.Acquire(runCtx)
			}
			if err != nil {
				break
			}
		}
		for _, c := range warmup {
			if c != nil {
				c.Release()
			}
		}
	}

	e.stats = NewStats()
	e.stats.Start()

	var wg sync.WaitGroup
	for i := range e.config.Concurrency {
		wg.Add(1)
		go func() {
			defer wg.Done()
			e.workerLoop(runCtx, cp, i)
		}()
	}

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

// workerLoop acquires a connection per transaction and releases it immediately
func (e *Engine) workerLoop(ctx context.Context, pool connPool, workerIdx int) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		if err := e.limiter.Wait(ctx); err != nil {
			return
		}

		var conn *pgxpool.Conn
		if pool != nil {
			var err error
			if dp, ok := pool.(*DiscoveryPool); ok {
				conn, err = dp.AcquireForWorker(ctx, workerIdx)
			} else {
				conn, err = pool.Acquire(ctx)
			}
			if err != nil {
				e.stats.Record(0, err)
				select {
				case <-ctx.Done():
					return
				case <-time.After(5 * time.Second):
				}
				continue
			}
		}

		start := time.Now()
		execErr := e.config.Workload.Execute(ctx, conn)
		e.stats.Record(time.Since(start), execErr)

		if conn != nil {
			conn.Release()
		}
	}
}

func (e *Engine) report(snap Snapshot) {
	if e.config.OnSnapshot != nil {
		e.config.OnSnapshot(snap)
	} else {
		fmt.Println(FormatSnapshot(snap))
	}
}
