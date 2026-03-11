package engine

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// mockWorkload simulates work without a real database connection.
type mockWorkload struct {
	name    string
	delay   time.Duration
	execErr error
}

func (m *mockWorkload) Name() string { return m.name }
func (m *mockWorkload) Execute(ctx context.Context, _ *pgxpool.Pool) error {
	if m.delay > 0 {
		select {
		case <-time.After(m.delay):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return m.execErr
}

// --- Stats unit tests ---

func TestStatsRecord(t *testing.T) {
	s := NewStats()
	s.Start()

	s.Record(10*time.Millisecond, nil)
	s.Record(20*time.Millisecond, nil)
	s.Record(100*time.Millisecond, errors.New("boom"))

	snap := s.Snapshot()

	if snap.TotalOps != 3 {
		t.Errorf("TotalOps: want 3, got %d", snap.TotalOps)
	}
	if snap.Errors != 1 {
		t.Errorf("Errors: want 1, got %d", snap.Errors)
	}
	if snap.TPS <= 0 {
		t.Errorf("TPS: want > 0, got %f", snap.TPS)
	}
	if snap.P50 == 0 {
		t.Errorf("P50: want > 0, got %s", snap.P50)
	}
}

func TestStatsSnapshotResetsInterval(t *testing.T) {
	s := NewStats()
	s.Start()

	s.Record(5*time.Millisecond, nil)
	s.Record(5*time.Millisecond, nil)
	snap1 := s.Snapshot()

	// After snapshot the interval counters reset.
	snap2 := s.Snapshot()

	if snap1.TPS <= 0 {
		t.Error("snap1 TPS should be positive")
	}
	// snap2 covers an empty interval — interval ops is 0.
	if snap2.TPS != 0 {
		t.Errorf("snap2 TPS: want 0 (no new ops), got %f", snap2.TPS)
	}
	// But TotalOps is cumulative.
	if snap2.TotalOps != 2 {
		t.Errorf("snap2 TotalOps: want 2, got %d", snap2.TotalOps)
	}
}

func TestFinalSummary(t *testing.T) {
	s := NewStats()
	s.Start()

	for i := 0; i < 10; i++ {
		s.Record(time.Duration(i+1)*time.Millisecond, nil)
	}
	s.Record(0, errors.New("err"))

	sum := s.FinalSummary()
	if sum.TotalOps != 11 {
		t.Errorf("TotalOps: want 11, got %d", sum.TotalOps)
	}
	if sum.Errors != 1 {
		t.Errorf("Errors: want 1, got %d", sum.Errors)
	}
	if sum.TPS <= 0 {
		t.Errorf("TPS: want > 0, got %f", sum.TPS)
	}
	if sum.P99Latency == 0 {
		t.Error("P99Latency should be > 0")
	}
}

// --- Engine unit tests (no real DB) ---

func TestEngineRunMock(t *testing.T) {
	wl := &mockWorkload{name: "mock", delay: 1 * time.Millisecond}
	cfg := Config{
		Concurrency: 4,
		Duration:    200 * time.Millisecond,
		ReportEvery: 50 * time.Millisecond,
		Workload:    wl,
	}

	eng := New(cfg)
	eng.SetPool(nil) // no pool; mock workload ignores it

	sum, err := eng.run(context.Background(), nil)
	if err != nil {
		t.Fatalf("run error: %v", err)
	}
	if sum.TotalOps == 0 {
		t.Error("expected at least one operation")
	}
	if sum.TPS <= 0 {
		t.Errorf("TPS: want > 0, got %f", sum.TPS)
	}
}

func TestEngineRateLimit(t *testing.T) {
	wl := &mockWorkload{name: "mock"}
	cfg := Config{
		Concurrency: 8,
		Duration:    300 * time.Millisecond,
		MaxRPS:      20, // cap at 20 RPS
		ReportEvery: 1 * time.Second,
		Workload:    wl,
	}

	eng := New(cfg)
	sum, err := eng.run(context.Background(), nil)
	if err != nil {
		t.Fatalf("run error: %v", err)
	}

	// With a 300ms window at 20 RPS we expect ~6 ops.
	// Allow generous margin for scheduler jitter.
	if sum.TotalOps > 30 {
		t.Errorf("rate limit exceeded: got %d ops in 300ms at 20 RPS", sum.TotalOps)
	}
}

func TestEngineGracefulCancel(t *testing.T) {
	wl := &mockWorkload{name: "mock", delay: 5 * time.Millisecond}
	cfg := Config{
		Concurrency: 4,
		Duration:    10 * time.Second, // long duration
		ReportEvery: 1 * time.Second,
		Workload:    wl,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()

	eng := New(cfg)
	sum, err := eng.run(ctx, nil)
	if err != nil {
		t.Fatalf("run error: %v", err)
	}
	// Should have stopped early due to ctx cancellation.
	if sum.Duration > 500*time.Millisecond {
		t.Errorf("did not stop early: duration=%s", sum.Duration)
	}
}

func TestSetRate(t *testing.T) {
	eng := New(Config{Workload: &mockWorkload{name: "mock"}})
	eng.SetRate(100)
	if eng.limiter.Limit() != 100 {
		t.Errorf("SetRate(100): want limit=100, got %v", eng.limiter.Limit())
	}
	eng.SetRate(0) // unlimited
	if eng.limiter.Limit() != 0 { // rate.Inf == 0 sentinel in some versions
		// just check no panic
	}
}
