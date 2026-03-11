package workload

import (
	"context"
	"fmt"
	"math/rand"

	"github.com/jackc/pgx/v5/pgxpool"
)

func init() {
	Register(&PgbenchSelect{ScaleFactor: 1})
}

// PgbenchSelect mirrors pgbench -S: a single point-lookup on pgbench_accounts.
// Use this to benchmark raw read throughput and compare against pgbench -S baselines.
type PgbenchSelect struct {
	ScaleFactor int
}

func (w *PgbenchSelect) Name() string { return "pgbench-select" }

// SetScaleFactor overrides the scale factor (called from CLI flag).
func (w *PgbenchSelect) SetScaleFactor(sf int) { w.ScaleFactor = sf }

// Prepare auto-detects scale factor from the DB when not explicitly set.
func (w *PgbenchSelect) Prepare(ctx context.Context, pool *pgxpool.Pool) error {
	if w.ScaleFactor > 1 {
		return nil
	}

	conn, err := pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()

	var sf int
	if err = conn.QueryRow(ctx, "SELECT count(*) FROM pgbench_branches").Scan(&sf); err != nil {
		return fmt.Errorf("detect scale factor: %w", err)
	}
	if sf >= 1 {
		w.ScaleFactor = sf
		fmt.Printf("[prepare] pgbench-select: detected scale factor = %d\n", sf)
	}
	return nil
}

func (w *PgbenchSelect) Execute(ctx context.Context, conn *pgxpool.Conn) error {
	sf := w.ScaleFactor
	if sf < 1 {
		sf = 1
	}

	aid := rand.Intn(100_000*sf) + 1

	var abalance int
	return conn.QueryRow(ctx,
		"SELECT abalance FROM pgbench_accounts WHERE aid = $1", aid,
	).Scan(&abalance)
}
