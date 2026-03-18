package workload

import (
	"context"
	"fmt"
	"math/rand"

	"github.com/jackc/pgx/v5/pgxpool"
)

// accountsPerBranch is the number of rows pgbench inserts into pgbench_accounts
// per branch at any scale factor (pgbench -s N always creates N*100_000 accounts).
// This is a pgbench schema invariant, not a tunable constant.
const accountsPerBranch = 100_000

func init() {
	Register(&PgbenchReadHeavy{ScaleFactor: 1})
}

// PgbenchReadHeavy is a read-only workload that mixes three query types of
// increasing cost to stress-test read replicas and drive up backend connection
// counts at moderate RPS. All queries target the standard pgbench schema.
//
// Query mix (per Execute call):
//
//	40% for account lookup       (~1-2 ms):   point lookup by primary key
//	35% for branch range report  (~5-15 ms):  5% of a branch sorted by balance
//	25% for branch financial summary (~15-40 ms): aggregate over 20% of a branch
//
// All range sizes are derived from accountsPerBranch so they stay proportional
// when the scale factor changes, no magic numbers in query methods.
type PgbenchReadHeavy struct {
	ScaleFactor int

	// Derived in Prepare() from ScaleFactor and pgbench schema invariants.
	// Fields are unexported because callers should set only ScaleFactor.
	totalAccounts int // accountsPerBranch * ScaleFactor
	scanSize      int // accountsPerBranch / 20  → 5% of one branch  (5,000 rows at sf=1)
	aggSize       int // accountsPerBranch / 5   → 20% of one branch (20,000 rows at sf=1)
}

func (w *PgbenchReadHeavy) Name() string { return "pgbench-read-heavy" }

// SetScaleFactor overrides the scale factor (called from CLI flag).
// Must be called before Prepare(); if > 1 it also suppresses auto-detection.
func (w *PgbenchReadHeavy) SetScaleFactor(sf int) { w.ScaleFactor = sf }

// Prepare auto-detects the scale factor from the DB when not explicitly set,
// then computes all derived query parameters from the pgbench schema invariants.
//
// Derived values:
//   - totalAccounts = accountsPerBranch * sf  (pgbench always creates sf*100K accounts)
//   - scanSize      = accountsPerBranch / 20  (5% of one branch, range query window)
//   - aggSize       = accountsPerBranch / 5   (20% of one branch, aggregation window)
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
	w.scanSize = accountsPerBranch / 20 // 5,000 at sf=1
	w.aggSize = accountsPerBranch / 5   // 20,000 at sf=1

	return nil
}

// Execute randomly dispatches to one of three query types based on configured ratios.
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

// accountLookup simulates a customer checking their account balance.
// Expected latency: ~1-2 ms (single index seek on pgbench_accounts_pkey).
func (w *PgbenchReadHeavy) accountLookup(ctx context.Context, conn *pgxpool.Conn) error {
	aid := rand.Intn(w.totalAccounts) + 1

	var aid2, bid, abalance int
	var filler string
	return conn.QueryRow(ctx,
		`SELECT aid, bid, abalance, filler FROM pgbench_accounts WHERE aid = $1`,
		aid,
	).Scan(&aid2, &bid, &abalance, &filler)
}

// branchRangeReport simulates a bank employee listing accounts within a branch,
// sorted by balance. Scans scanSize rows (5% of one branch) via an index range
// scan, then sorts, generating moderate I/O and CPU pressure.
// Expected latency: ~5-15 ms.
func (w *PgbenchReadHeavy) branchRangeReport(ctx context.Context, conn *pgxpool.Conn) error {
	startAID := rand.Intn(w.totalAccounts-w.scanSize) + 1

	rows, err := conn.Query(ctx,
		`SELECT aid, bid, abalance
		 FROM pgbench_accounts
		 WHERE aid BETWEEN $1 AND $1 + $2 - 1
		 ORDER BY abalance DESC`,
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

// branchFinancialSummary simulates a manager reviewing financial statistics for
// a contiguous block of accounts (20% of one branch). Scans aggSize rows and
// computes six aggregates, the heaviest query in the mix.
// Expected latency: ~15-40 ms.
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
