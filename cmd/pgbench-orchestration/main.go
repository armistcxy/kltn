package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/armistcxy/kltn/pkg/pgbench"
	"golang.org/x/sync/errgroup"
)

func main() {
	workerAddresses := []string{}
	f, err := os.Open("address.json")
	if err != nil {
		panic("failed to open address.json: " + err.Error())
	}
	defer f.Close()
	if err := json.NewDecoder(f).Decode(&workerAddresses); err != nil {
		panic("failed to decode address.json: " + err.Error())
	}
	log.Printf("Loaded worker addresses: %+v\n", workerAddresses)

	orchestrator := NewOrchestrator(workerAddresses)

	http.HandleFunc("POST /benchmark", orchestrator.HandleBenchmark)
	fmt.Println("Starting pgbench orchestrator on :8080")

	if err := http.ListenAndServe(":8080", nil); err != nil {
		fmt.Printf("failed to start server: %v\n", err)
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

type BenchmarkResponse struct {
	Results []struct {
		Address string                       `json:"address"`
		Result  pgbench.PGBenchExecuteResult `json:"result"`
	}

	TotalTransactionsCompleted int     `json:"total_transactions_completed"`
	TotalTPS                   float64 `json:"total_tps"`

	AverageLatency float64 `json:"average_latency,omitempty"`
}

type Orchestrator struct {
	WorkerAddresses []string
}

func NewOrchestrator(workerAddresses []string) *Orchestrator {
	return &Orchestrator{
		WorkerAddresses: workerAddresses,
	}
}

func (o *Orchestrator) HandleBenchmark(w http.ResponseWriter, r *http.Request) {
	var req BenchmarkRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request payload", http.StatusBadRequest)
		return
	}

	clientsPerWorker := req.Clients / len(o.WorkerAddresses)
	assignedClients := make([]int, len(o.WorkerAddresses))
	for i := range o.WorkerAddresses {
		assignedClients[i] = clientsPerWorker
	}
	// Distribute remaining clients
	for i := 0; i < req.Clients%len(o.WorkerAddresses); i++ {
		assignedClients[i]++
	}

	threadsPerWorker := min(1, clientsPerWorker/8)
	assignedThreads := make([]int, len(o.WorkerAddresses))
	for i := range o.WorkerAddresses {
		assignedThreads[i] = threadsPerWorker
	}
	// Distribute remaining threads
	for i := 0; i < clientsPerWorker%8 && i < len(o.WorkerAddresses); i++ {
		assignedThreads[i]++
	}

	results := make([]pgbench.PGBenchExecuteResult, len(o.WorkerAddresses))

	var errGroup errgroup.Group
	for i, addr := range o.WorkerAddresses {
		workerReq := BenchmarkRequest{
			DatabaseHost:      req.DatabaseHost,
			DatabasePort:      req.DatabasePort,
			DatabaseUser:      req.DatabaseUser,
			DatabasePassword:  req.DatabasePassword,
			DatabaseName:      req.DatabaseName,
			Clients:           assignedClients[i],
			Threads:           assignedThreads[i],
			DurationSeconds:   req.DurationSeconds,
			FileLoggingPrefix: "tmp/pgbench_worker",
		}

		errGroup.Go(func() error {
			jsonData, err := json.Marshal(workerReq)
			if err != nil {
				return err
			}

			resp, err := http.Post("http://"+addr+":8080/benchmark", "application/json", bytes.NewBuffer(jsonData))
			if err != nil {
				return err
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusOK {
				return fmt.Errorf("worker %s returned status %d", addr, resp.StatusCode)
			}

			var workerResult pgbench.PGBenchExecuteResult
			if err := json.NewDecoder(resp.Body).Decode(&workerResult); err != nil {
				return err
			}

			results[i] = workerResult

			return nil
		})

	}

	err := errGroup.Wait()
	if err != nil {
		http.Error(w, "benchmark execution failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	var response BenchmarkResponse
	for i, res := range results {
		response.Results = append(response.Results, struct {
			Address string                       `json:"address"`
			Result  pgbench.PGBenchExecuteResult `json:"result"`
		}{
			Address: o.WorkerAddresses[i],
			Result:  res,
		})
		response.TotalTPS += res.TPS
		response.TotalTransactionsCompleted += res.TransactionsCompleted
	}

	if err := json.NewEncoder(w).Encode(response); err != nil {
		http.Error(w, "failed to encode response: "+err.Error(), http.StatusInternalServerError)
		return
	}
}
