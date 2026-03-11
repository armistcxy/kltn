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

func (w *PgbenchSelect) Execute(ctx context.Context, pool *pgxpool.Pool) error {
	sf := w.ScaleFactor
	if sf < 1 {
		sf = 1
	}

	aid := rand.Intn(100_000*sf) + 1

	conn, err := pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("acquire: %w", err)
	}
	defer conn.Release()

	var abalance int
	return conn.QueryRow(ctx,
		"SELECT abalance FROM pgbench_accounts WHERE aid = $1", aid,
	).Scan(&abalance)
}
