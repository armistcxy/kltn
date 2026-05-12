package engine

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	hdrhistogram "github.com/HdrHistogram/hdrhistogram-go"
)

// Stats tracks latency and throughput using an HDR histogram.
//
// cumulative for the final summary
//
// per-interval (reset on each Snapshot() call) for live reporting
type Stats struct {
	mu           sync.Mutex
	cumHist      *hdrhistogram.Histogram
	intervalHist *hdrhistogram.Histogram

	totalOps    atomic.Int64
	totalErrors atomic.Int64
	intervalOps atomic.Int64
	intervalErr atomic.Int64

	startTime     time.Time
	intervalStart time.Time
}

// Snapshot is a point-in-time view of the load metrics over the last interval
type Snapshot struct {
	Elapsed  time.Duration
	TPS      float64
	P50      time.Duration
	P95      time.Duration
	P99      time.Duration
	P999     time.Duration
	Errors   int64
	TotalOps int64
}

// Summary is the aggregate result for the entire run.
type Summary struct {
	Duration    time.Duration
	TotalOps    int64
	Errors      int64
	TPS         float64
	P50Latency  time.Duration
	P95Latency  time.Duration
	P99Latency  time.Duration
	P999Latency time.Duration
}

func NewStats() *Stats {
	return &Stats{
		cumHist:      hdrhistogram.New(1, 3_600_000_000, 3),
		intervalHist: hdrhistogram.New(1, 3_600_000_000, 3),
	}
}

func (s *Stats) Start() {
	now := time.Now()
	s.startTime = now
	s.intervalStart = now
}

// Record adds a single observation
func (s *Stats) Record(latency time.Duration, err error) {
	us := latency.Microseconds()
	if us < 1 {
		us = 1
	}

	s.mu.Lock()
	_ = s.cumHist.RecordValue(us)
	_ = s.intervalHist.RecordValue(us)
	s.mu.Unlock()

	s.totalOps.Add(1)
	s.intervalOps.Add(1)
	if err != nil {
		s.totalErrors.Add(1)
		s.intervalErr.Add(1)
	}
}

func (s *Stats) Snapshot() Snapshot {
	now := time.Now()

	s.mu.Lock()
	h := hdrhistogram.Import(s.intervalHist.Export())
	s.intervalHist.Reset()
	s.mu.Unlock()

	intervalOps := s.intervalOps.Swap(0)
	intervalErr := s.intervalErr.Swap(0)

	elapsed := now.Sub(s.intervalStart)
	s.intervalStart = now

	tps := 0.0
	if elapsed > 0 {
		tps = float64(intervalOps) / elapsed.Seconds()
	}

	return Snapshot{
		Elapsed:  now.Sub(s.startTime),
		TPS:      tps,
		P50:      time.Duration(h.ValueAtQuantile(50)) * time.Microsecond,
		P95:      time.Duration(h.ValueAtQuantile(95)) * time.Microsecond,
		P99:      time.Duration(h.ValueAtQuantile(99)) * time.Microsecond,
		P999:     time.Duration(h.ValueAtQuantile(99.9)) * time.Microsecond,
		Errors:   intervalErr,
		TotalOps: s.totalOps.Load(),
	}
}

// FinalSummary returns aggregate stats for the full run
func (s *Stats) FinalSummary() *Summary {
	now := time.Now()
	elapsed := now.Sub(s.startTime)

	s.mu.Lock()
	h := hdrhistogram.Import(s.cumHist.Export())
	s.mu.Unlock()

	total := s.totalOps.Load()
	tps := 0.0
	if elapsed > 0 {
		tps = float64(total) / elapsed.Seconds()
	}

	return &Summary{
		Duration:    elapsed,
		TotalOps:    total,
		Errors:      s.totalErrors.Load(),
		TPS:         tps,
		P50Latency:  time.Duration(h.ValueAtQuantile(50)) * time.Microsecond,
		P95Latency:  time.Duration(h.ValueAtQuantile(95)) * time.Microsecond,
		P99Latency:  time.Duration(h.ValueAtQuantile(99)) * time.Microsecond,
		P999Latency: time.Duration(h.ValueAtQuantile(99.9)) * time.Microsecond,
	}
}

// FormatSnapshot produces a single-line console report for a snapshot
func FormatSnapshot(s Snapshot) string {
	return fmt.Sprintf(
		"[%5s] TPS: %7.1f | P50: %7s | P95: %7s | P99: %7s | Errors: %d | Total: %d",
		s.Elapsed.Round(time.Second),
		s.TPS,
		fmtDur(s.P50),
		fmtDur(s.P95),
		fmtDur(s.P99),
		s.Errors,
		s.TotalOps,
	)
}

func fmtDur(d time.Duration) string {
	switch {
	case d >= time.Second:
		return fmt.Sprintf("%.2fs", d.Seconds())
	case d >= time.Millisecond:
		return fmt.Sprintf("%.2fms", float64(d)/float64(time.Millisecond))
	default:
		return fmt.Sprintf("%.0fµs", float64(d)/float64(time.Microsecond))
	}
}
