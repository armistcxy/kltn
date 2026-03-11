package workload

import (
	"context"
	"fmt"
	"math/rand"

	"github.com/jackc/pgx/v5/pgxpool"
)

func init() {
	Register(&PgbenchOLTP{ScaleFactor: 1})
}

// PgbenchOLTP implements the standard pgbench TPC-B-like transaction:
//  1. UPDATE pgbench_accounts
//  2. SELECT pgbench_accounts
//  3. UPDATE pgbench_tellers
//  4. UPDATE pgbench_branches
//  5. INSERT pgbench_history
//
// Requires the pgbench schema to be initialised in the target database:
//
//	pgbench -i -s <scale> <connstring>
type PgbenchOLTP struct {
	// ScaleFactor matches the -s flag used during pgbench -i.
	ScaleFactor int
}

func (w *PgbenchOLTP) Name() string { return "pgbench-oltp" }

// SetScaleFactor overrides the scale factor (called from CLI flag).
func (w *PgbenchOLTP) SetScaleFactor(sf int) { w.ScaleFactor = sf }

// Prepare auto-detects scale factor from the DB when it was not explicitly set
// via SetScaleFactor. Critical: with sf=1 all transactions contend on a single
// pgbench_branches row, serialising throughput regardless of concurrency.
func (w *PgbenchOLTP) Prepare(ctx context.Context, pool *pgxpool.Pool) error {
	if w.ScaleFactor > 1 {
		return nil // user set it explicitly; trust them
	}

	conn, err := pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()

	var sf int
	if err = conn.QueryRow(ctx, "SELECT count(*) FROM pgbench_branches").Scan(&sf); err != nil {
		return fmt.Errorf("detect scale factor (did you run `pgbench -i -s N`?): %w", err)
	}
	if sf < 1 {
		return fmt.Errorf("pgbench_branches is empty; run pgbench -i -s N first")
	}
	w.ScaleFactor = sf
	fmt.Printf("[prepare] pgbench-oltp: detected scale factor = %d\n", sf)
	return nil
}

func (w *PgbenchOLTP) Execute(ctx context.Context, conn *pgxpool.Conn) error {
	sf := w.ScaleFactor
	if sf < 1 {
		sf = 1
	}

	aid := rand.Intn(100_000*sf) + 1
	tid := rand.Intn(10*sf) + 1
	bid := rand.Intn(sf) + 1
	delta := rand.Intn(10001) - 5000

	tx, err := conn.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	if _, err = tx.Exec(ctx,
		"UPDATE pgbench_accounts SET abalance = abalance + $1 WHERE aid = $2",
		delta, aid); err != nil {
		return fmt.Errorf("update accounts: %w", err)
	}

	var abalance int
	if err = tx.QueryRow(ctx,
		"SELECT abalance FROM pgbench_accounts WHERE aid = $1", aid).Scan(&abalance); err != nil {
		return fmt.Errorf("select accounts: %w", err)
	}

	if _, err = tx.Exec(ctx,
		"UPDATE pgbench_tellers SET tbalance = tbalance + $1 WHERE tid = $2",
		delta, tid); err != nil {
		return fmt.Errorf("update tellers: %w", err)
	}

	if _, err = tx.Exec(ctx,
		"UPDATE pgbench_branches SET bbalance = bbalance + $1 WHERE bid = $2",
		delta, bid); err != nil {
		return fmt.Errorf("update branches: %w", err)
	}

	if _, err = tx.Exec(ctx,
		"INSERT INTO pgbench_history (tid, bid, aid, delta, mtime) VALUES ($1, $2, $3, $4, CURRENT_TIMESTAMP)",
		tid, bid, aid, delta); err != nil {
		return fmt.Errorf("insert history: %w", err)
	}

	return tx.Commit(ctx)
}
