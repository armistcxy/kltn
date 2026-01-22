package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"

	"github.com/armistcxy/kltn/pkg/pgbench"
)

func main() {
	http.HandleFunc("/benchmark", HandleBenchmark)
	log.Println("Starting pgbench agent on :8080")
	if err := http.ListenAndServe(":8080", nil); err != nil {
		log.Fatalf("failed to start server: %v", err)
	}
}

type BenchmarkRequest struct {
	DatabaseHost     string `json:"database_host"`
	DatabasePort     int    `json:"database_port"`
	DatabaseUser     string `json:"database_user"`
	DatabasePassword string `json:"database_password"`
	DatabaseName     string `json:"database_name"`

	Clients         int `json:"clients"`
	Threads         int `json:"threads"`
	DurationSeconds int `json:"duration_seconds"`

	FileLoggingPrefix string `json:"file_logging_prefix"`
}

func HandleBenchmark(w http.ResponseWriter, r *http.Request) {
	var req BenchmarkRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request payload", http.StatusBadRequest)
		return
	}

	log.Printf("Received benchmark request: %+v", req)

	benchmarkResult, err := pgbench.ExecutePGBench(context.TODO(), pgbench.PGBenchConfig{
		DatabaseHost:      req.DatabaseHost,
		DatabasePort:      req.DatabasePort,
		DatabaseUser:      req.DatabaseUser,
		DatabasePassword:  req.DatabasePassword,
		DatabaseName:      req.DatabaseName,
		Clients:           req.Clients,
		Threads:           req.Threads,
		DurationSeconds:   req.DurationSeconds,
		FileLoggingPrefix: req.FileLoggingPrefix,
	})
	if err != nil {
		http.Error(w, "benchmark execution failed: "+err.Error(), http.StatusInternalServerError)
		log.Printf("Benchmark execution failed: %v", err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(benchmarkResult); err != nil {
		http.Error(w, "failed to encode response: "+err.Error(), http.StatusInternalServerError)
		log.Printf("Failed to encode response: %v", err)
		return
	}
}
