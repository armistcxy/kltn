package pgbench

import (
	"bufio"
	"context"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// PGBenchConfig holds the configuration for pgbench benchmarks
type PGBenchConfig struct {
	// Database connection settings
	DatabaseHost     string
	DatabasePort     int
	DatabaseUser     string
	DatabasePassword string
	DatabaseName     string

	// Benchmark settings
	DurationSeconds int
	Clients         int
	Threads         int

	// Output settings
	FileLoggingPrefix string
}

// PGBenchExecuteResult holds the results of a pgbench execution
type PGBenchExecuteResult struct {
	TransactionsCompleted int     `json:"transactions_completed"`
	TPS                   float64 `json:"tps"`

	// Latency metrics (in milliseconds)
	AverageLatency float64 `json:"average_latency"`
	P90Latency     float64 `json:"p90_latency"`
	P95Latency     float64 `json:"p95_latency"`
	P99Latency     float64 `json:"p99_latency"`
}

// ExecutePGBench runs the pgbench benchmark with the given configuration
func ExecutePGBench(ctx context.Context, config PGBenchConfig) (*PGBenchExecuteResult, error) {
	cmd := exec.Command(
		"pgbench",
	)
	if config.DatabaseHost != "" {
		cmd.Args = append(cmd.Args, "-h", config.DatabaseHost)
	}
	if config.DatabasePort != 0 {
		cmd.Args = append(cmd.Args, "-p", strconv.Itoa(config.DatabasePort))
	}
	if config.DatabaseUser != "" {
		cmd.Args = append(cmd.Args, "-U", config.DatabaseUser)
	}
	if config.Clients != 0 {
		cmd.Args = append(cmd.Args, "-c", strconv.Itoa(config.Clients))
	}
	if config.Threads != 0 {
		cmd.Args = append(cmd.Args, "-j", strconv.Itoa(config.Threads))
	}
	if config.DurationSeconds != 0 {
		cmd.Args = append(cmd.Args, "-T", strconv.Itoa(config.DurationSeconds))
	}
	cmd.Args = append(cmd.Args, "--log")

	// If no file logging prefix is provided, use a default one
	if config.FileLoggingPrefix == "" {
		config.FileLoggingPrefix = "tmp/pgbench_log"
		// Ensure the tmp directory exists
		if err := os.MkdirAll("tmp", 0755); err != nil {
			return nil, err
		}
	}
	cmd.Args = append(cmd.Args, "--log-prefix="+config.FileLoggingPrefix)

	if config.DatabaseName == "" {
		config.DatabaseName = "postgres"
	}
	cmd.Args = append(cmd.Args, config.DatabaseName)

	if config.DatabasePassword != "" {
		cmd.Env = append(cmd.Env, "PGPASSWORD="+config.DatabasePassword)
	}

	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, err
	}
	parseReportResult, err := parsePGBenchReport(out)
	if err != nil {
		return nil, err
	}

	parseLogResult, err := parsePGBenchLogs(config.FileLoggingPrefix)
	if err != nil {
		return nil, err
	}

	return &PGBenchExecuteResult{
		TransactionsCompleted: parseReportResult.TransactionsCompleted,
		TPS:                   parseReportResult.TPS,
		AverageLatency:        parseLogResult.AverageLatency,
		P90Latency:            parseLogResult.P90Latency,
		P95Latency:            parseLogResult.P95Latency,
		P99Latency:            parseLogResult.P99Latency,
	}, nil
}

type ParsePGBenchLogsResult struct {
	AverageLatency float64
	P90Latency     float64
	P95Latency     float64
	P99Latency     float64
}

func parsePGBenchLogs(prefix string) (*ParsePGBenchLogsResult, error) {
	files, err := filepath.Glob(prefix + ".*")
	if err != nil {
		return nil, err
	}

	var latencies []float64

	for _, file := range files {
		f, err := os.Open(file)
		if err != nil {
			panic("failed to open log file: " + err.Error())
		}
		defer f.Close()

		scanner := bufio.NewScanner(f)

		for scanner.Scan() {
			line := scanner.Text()
			fields := strings.Fields(line)
			if len(fields) < 6 {
				continue
			}

			latency, err := strconv.ParseFloat(fields[len(fields)-1], 64)
			if err != nil {
				continue
			}
			latencyMs := latency / 1000.0 // Convert to milliseconds
			latencies = append(latencies, latencyMs)
		}
	}

	defer func() {
		for _, file := range files {
			if err := os.Remove(file); err != nil {
				log.Printf("failed to remove log file %s: %v\n", file, err)
			}
		}
	}()

	sort.Float64s(latencies)

	return &ParsePGBenchLogsResult{
		AverageLatency: percentile(latencies, 50),
		P90Latency:     percentile(latencies, 90),
		P95Latency:     percentile(latencies, 95),
		P99Latency:     percentile(latencies, 99),
	}, nil
}

func percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(float64(len(sorted))*p/100) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}

type ParsePGBenchReport struct {
	TransactionsCompleted int
	TPS                   float64
}

func parsePGBenchReport(output []byte) (*ParsePGBenchReport, error) {
	lines := strings.Split(string(output), "\n")
	var result ParsePGBenchReport

	for _, line := range lines {
		if strings.Contains(line, "number of transactions actually processed:") {
			parts := strings.Split(line, ":")
			if len(parts) == 2 {
				countStr := strings.TrimSpace(parts[1])
				count, err := strconv.Atoi(countStr)
				if err != nil {
					return nil, err
				}
				result.TransactionsCompleted = count
			}
		} else if strings.Contains(line, "tps =") {
			parts := strings.Split(line, "=")
			if len(parts) >= 2 {
				tpsPart := strings.TrimSpace(parts[1])
				tpsFields := strings.Fields(tpsPart)
				if len(tpsFields) >= 1 {
					tpsStr := tpsFields[0]
					tps, err := strconv.ParseFloat(tpsStr, 64)
					if err != nil {
						return nil, err
					}
					result.TPS = tps
				}
			}
		}
	}

	return &result, nil
}
