package pgbench

import (
	"context"
	"os"
	"strconv"
	"testing"
)

func TestExecutePGBench(t *testing.T) {
	var (
		host = os.Getenv("PG_HOST")
		port = os.Getenv("PG_PORT")
		user = os.Getenv("PG_USER")
		pass = os.Getenv("PG_PASSWORD")
		db   = os.Getenv("PG_DATABASE")
	)

	if host == "" || port == "" || user == "" || pass == "" || db == "" {
		t.Skip("Skipping test; PG_HOST, PG_PORT, PG_USER, PG_PASSWORD, or PG_DATABASE environment variables are not set")
	}

	portInt, err := strconv.Atoi(port)
	if err != nil {
		t.Fatalf("Invalid PG_PORT: %v", err)
	}

	if err := os.MkdirAll("tmp", 0755); err != nil {
		t.Fatalf("Failed to create tmp directory: %v", err)
	}

	config := PGBenchConfig{
		DatabaseHost:      host,
		DatabasePort:      portInt,
		DatabaseUser:      user,
		DatabaseName:      db,
		DatabasePassword:  pass,
		Clients:           30,
		Threads:           4,
		DurationSeconds:   30,
		FileLoggingPrefix: "tmp/pgbench_test_log",
	}
	result, err := ExecutePGBench(context.TODO(), config)
	if err != nil {
		t.Fatalf("ExecutePGBench failed: %v", err)
	}

	t.Logf("Transactions Completed: %d", result.TransactionsCompleted)
	t.Logf("TPS: %.2f", result.TPS)
	t.Logf("Average Latency: %.2f ms", result.AverageLatency)
	t.Logf("P90 Latency: %.2f ms", result.P90Latency)
	t.Logf("P95 Latency: %.2f ms", result.P95Latency)
	t.Logf("P99 Latency: %.2f ms", result.P99Latency)
}
