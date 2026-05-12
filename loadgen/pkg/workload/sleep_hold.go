package workload

import (
	"context"
	"os"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func init() {
	Register(&SleepHold{sleepDuration: defaultSleepDuration()})
}

func defaultSleepDuration() float64 {
	if v := os.Getenv("SLEEP_HOLD_SECONDS"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil && f > 0 {
			return f
		}
	}
	return 0.1
}

// SleepHold holds a connection for a configurable duration without consuming CPU
type SleepHold struct {
	sleepDuration float64
}

func (w *SleepHold) Name() string { return "sleep-hold" }

func (w *SleepHold) Execute(ctx context.Context, conn *pgxpool.Conn) error {
	d := time.Duration(w.sleepDuration * float64(time.Second))
	_, err := conn.Exec(ctx, "SELECT pg_sleep($1)", d.Seconds())
	return err
}
