package workload

import (
	"context"
	"fmt"
	"math/rand"

	"github.com/jackc/pgx/v5/pgxpool"
)

// accountsPerBranch is the number of rows pgbench inserts into pgbench_accounts
// per branch at any scale factor (pgbench -s N always creates N*100_000 accounts).
const accountsPerBranch = 100_000

func init() {
	Register(&PgbenchReadHeavy{ScaleFactor: 1})
}

// PgbenchReadHeavy is a read-only workload that mixes three query types
type PgbenchReadHeavy struct {
	ScaleFactor   int
	totalAccounts int // accountsPerBranch * ScaleFactor
	scanSize      int // accountsPerBranch / 20 -> 5% of one branch (5,000 rows at sf=1)
	aggSize       int // accountsPerBranch / 5 -> 20% of one branch (20,000 rows at sf=1)
}

func (w *PgbenchReadHeavy) Name() string          { return "pgbench-read-heavy" }
func (w *PgbenchReadHeavy) SetScaleFactor(sf int) { w.ScaleFactor = sf }

func (w *PgbenchReadHeavy) Prepare(ctx context.Context, pool *pgxpool.Pool) error {
	if w.ScaleFactor <= 1 {
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
			fmt.Printf("[prepare] pgbench-read-heavy: detected scale factor = %d\n", sf)
		}
	}

	sf := w.ScaleFactor
	if sf < 1 {
		sf = 1
	}
	w.totalAccounts = accountsPerBranch * sf
	w.scanSize = accountsPerBranch / 20
	w.aggSize = accountsPerBranch / 5

	return nil
}

func (w *PgbenchReadHeavy) Execute(ctx context.Context, conn *pgxpool.Conn) error {
	r := rand.Float64()
	switch {
	case r < 0.40:
		return w.accountLookup(ctx, conn)
	case r < 0.75:
		return w.branchRangeReport(ctx, conn)
	default:
		return w.branchFinancialSummary(ctx, conn)
	}
}

// accountLookup simulates a customer checking their account balance
func (w *PgbenchReadHeavy) accountLookup(ctx context.Context, conn *pgxpool.Conn) error {
	aid := rand.Intn(w.totalAccounts) + 1

	var aid2, bid, abalance int
	var filler string
	return conn.QueryRow(ctx,
		`SELECT aid, bid, abalance, filler FROM pgbench_accounts WHERE aid = $1`,
		aid,
	).Scan(&aid2, &bid, &abalance, &filler)
}

// branchRangeReport simulates a bank employee listing accounts within a branch
func (w *PgbenchReadHeavy) branchRangeReport(ctx context.Context, conn *pgxpool.Conn) error {
	startAID := rand.Intn(w.totalAccounts-w.scanSize) + 1

	rows, err := conn.Query(ctx,
		`SELECT aid, bid, abalance
		 FROM pgbench_accounts
		 WHERE aid BETWEEN $1 AND $1 + $2 - 1
		 ORDER BY abalance DESC
		 LIMIT 100`,
		startAID, w.scanSize,
	)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var aid, bid, abalance int
		if err := rows.Scan(&aid, &bid, &abalance); err != nil {
			return err
		}
	}
	return rows.Err()
}

// branchFinancialSummary simulates a manager reviewing financial statistics for a contiguous block of accounts (20% of one branch)
func (w *PgbenchReadHeavy) branchFinancialSummary(ctx context.Context, conn *pgxpool.Conn) error {
	startAID := rand.Intn(w.totalAccounts-w.aggSize) + 1

	var totalAccounts int64
	var avgBalance, minBalance, maxBalance, totalBalance, balanceStddev float64
	return conn.QueryRow(ctx,
		`SELECT count(*)              AS total_accounts,
		        avg(abalance)         AS avg_balance,
		        min(abalance)         AS min_balance,
		        max(abalance)         AS max_balance,
		        sum(abalance)         AS total_balance,
		        stddev_samp(abalance) AS balance_stddev
		 FROM pgbench_accounts
		 WHERE aid BETWEEN $1 AND $1 + $2 - 1`,
		startAID, w.aggSize,
	).Scan(&totalAccounts, &avgBalance, &minBalance, &maxBalance, &totalBalance, &balanceStddev)
}
