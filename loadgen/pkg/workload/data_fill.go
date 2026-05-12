package workload

import (
	"context"
	cryptorand "crypto/rand"
	"fmt"
	"io"
	"math/rand"
	"os"
	"strconv"
	"sync/atomic"

	"github.com/jackc/pgx/v5/pgxpool"
)

func init() {
	Register(&DataFill{
		PayloadBytes: 8192,
		BloatRate:    parseBloatRate(),
	})
}

// DataFill grows a dedicated table at a controllable rate by inserting fixed-size rows of incompressible random bytes
type DataFill struct {
	PayloadBytes int // fixed row payload size in bytes (scaled by SetScaleFactor)
	BloatRate    float64

	tableHasRows atomic.Bool
	maxID        atomic.Int64
}

func (w *DataFill) Name() string { return "data-fill" }

func (w *DataFill) SetScaleFactor(sf int) {
	if sf < 1 {
		sf = 1
	}
	w.PayloadBytes = 8192 * sf
}

func (w *DataFill) Prepare(ctx context.Context, pool *pgxpool.Pool) error {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()

	_, err = conn.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS fill_data (
			id         BIGSERIAL   PRIMARY KEY,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			tag        TEXT        NOT NULL,
			payload    BYTEA       NOT NULL
		)`)
	if err != nil {
		return fmt.Errorf("create fill_data: %w", err)
	}

	var maxID int64
	if err = conn.QueryRow(ctx, `SELECT COALESCE(MAX(id), 0) FROM fill_data`).Scan(&maxID); err != nil {
		return fmt.Errorf("query max id: %w", err)
	}

	w.maxID.Store(maxID)
	if maxID > 0 {
		w.tableHasRows.Store(true)
	}

	fmt.Printf("[prepare] data-fill: maxID=%d tableHasRows=%v PayloadBytes=%d BloatRate=%.2f fill_rate=RPS×%dB\n",
		maxID, maxID > 0, w.PayloadBytes, w.BloatRate, w.PayloadBytes)
	return nil
}

var fillTags = []string{"user_data", "audit_log", "session", "blob", "export", "archive"}

func (w *DataFill) Execute(ctx context.Context, conn *pgxpool.Conn) error {
	if w.tableHasRows.Load() && rand.Float64() < w.BloatRate {
		return w.doUpdate(ctx, conn)
	}
	return w.doInsert(ctx, conn)
}

func (w *DataFill) doInsert(ctx context.Context, conn *pgxpool.Conn) error {
	payload, err := w.makePayload()
	if err != nil {
		return err
	}
	tag := fillTags[rand.Intn(len(fillTags))]

	var newID int64
	if err = conn.QueryRow(ctx,
		`INSERT INTO fill_data (tag, payload) VALUES ($1, $2) RETURNING id`,
		tag, payload,
	).Scan(&newID); err != nil {
		return fmt.Errorf("insert fill_data: %w", err)
	}

	for {
		cur := w.maxID.Load()
		if newID <= cur {
			break
		}
		if w.maxID.CompareAndSwap(cur, newID) {
			break
		}
	}
	w.tableHasRows.Store(true)
	return nil
}

func (w *DataFill) doUpdate(ctx context.Context, conn *pgxpool.Conn) error {
	maxID := w.maxID.Load()
	if maxID <= 0 {
		return w.doInsert(ctx, conn)
	}

	payload, err := w.makePayload()
	if err != nil {
		return err
	}

	targetID := rand.Int63n(maxID) + 1
	if _, err = conn.Exec(ctx,
		`UPDATE fill_data SET payload=$1, updated_at=NOW() WHERE id=$2`,
		payload, targetID,
	); err != nil {
		return fmt.Errorf("update fill_data: %w", err)
	}
	return nil
}

func (w *DataFill) makePayload() ([]byte, error) {
	buf := make([]byte, w.PayloadBytes)
	if _, err := io.ReadFull(cryptorand.Reader, buf); err != nil {
		return nil, fmt.Errorf("generate payload: %w", err)
	}
	return buf, nil
}

func parseBloatRate() float64 {
	s := os.Getenv("DATA_FILL_BLOAT_RATE")
	if s == "" {
		return 0.1
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || v < 0 || v >= 1 {
		fmt.Fprintf(os.Stderr, "[data-fill] invalid DATA_FILL_BLOAT_RATE=%q, using 0.1\n", s)
		return 0.1
	}
	return v
}
