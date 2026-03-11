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
	// Determines the number of rows in each table:
	//   accounts = 100_000 * sf, tellers = 10 * sf, branches = sf
	ScaleFactor int
}

func (w *PgbenchOLTP) Name() string { return "pgbench-oltp" }

func (w *PgbenchOLTP) Execute(ctx context.Context, pool *pgxpool.Pool) error {
	sf := w.ScaleFactor
	if sf < 1 {
		sf = 1
	}

	aid := rand.Intn(100_000*sf) + 1
	tid := rand.Intn(10*sf) + 1
	bid := rand.Intn(sf) + 1
	delta := rand.Intn(10001) - 5000

	conn, err := pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("acquire: %w", err)
	}
	defer conn.Release()

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
