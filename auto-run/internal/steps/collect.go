package steps

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// collectStepSeconds is the Prometheus query_range step used when collecting metrics.
const collectStepSeconds = 15

// metricQuery pairs a CSV filename with a PromQL expression.
type metricQuery struct {
	Filename string
	PromQL   string
}

// queriesForRun returns the list of Prometheus queries to run after a benchmark.
// Metric names match the scale-controller's exported gauges (see internal/scale/metrics.go).
var queriesForRun = []metricQuery{
	{
		Filename: "replicas.csv",
		PromQL:   `scaling_instances_current`,
	},
	{
		Filename: "replicas_target_final.csv",
		PromQL:   `scaling_instances_target_final`,
	},
	{
		Filename: "replicas_target_reactive.csv",
		PromQL:   `scaling_instances_target_reactive`,
	},
	{
		Filename: "replicas_target_predictive.csv",
		PromQL:   `scaling_instances_target_predictive`,
	},
	{
		Filename: "backends.csv",
		PromQL:   `sum(cnpg_backends_total{namespace="default",pod=~"pg-cluster-.*"})`,
	},
	{
		Filename: "tps.csv",
		PromQL: `sum(rate(cnpg_pg_stat_database_xact_commit{namespace="default",pod=~"pg-cluster-.*"}[1m]))` +
			` + sum(rate(cnpg_pg_stat_database_xact_rollback{namespace="default",pod=~"pg-cluster-.*"}[1m]))`,
	},
	{
		Filename: "metric_raw_backends.csv",
		PromQL:   `scaling_observer_metric_value{metric_name="backends"}`,
	},
	{
		Filename: "metric_raw_tps.csv",
		PromQL:   `scaling_observer_metric_value{metric_name="tps"}`,
	},
	{
		Filename: "metric_raw_avg_latency.csv",
		PromQL:   `scaling_observer_metric_value{metric_name="avg_latency"}`,
	},
}

// CollectMetrics queries Prometheus for the run window and writes CSV files
// plus the controller pod logs into rc.ResultsDir.
func CollectMetrics(
	ctx context.Context,
	rc *RunContext,
	clientset kubernetes.Interface,
	startTS, endTS time.Time,
) error {
	const stepName = "collect-metrics"
	const step = "15s"

	rc.Logf("[%s] querying Prometheus %s for window [%s – %s]",
		stepName, rc.PrometheusURL,
		startTS.Format(time.RFC3339),
		endTS.Format(time.RFC3339),
	)

	if err := os.MkdirAll(rc.ResultsDir, 0o755); err != nil {
		return fmt.Errorf("[%s] mkdir results: %w", stepName, err)
	}

	for _, q := range queriesForRun {
		if err := queryAndSave(ctx, rc.PrometheusURL, q.PromQL, startTS, endTS, step,
			filepath.Join(rc.ResultsDir, q.Filename),
		); err != nil {
			rc.Logf("[%s] warn: %s: %v", stepName, q.Filename, err)
		} else {
			rc.Logf("[%s] saved %s", stepName, q.Filename)
		}
	}

	// Collect controller pod logs.
	if err := collectControllerLogsToFile(ctx, rc, clientset, stepName); err != nil {
		rc.Logf("[%s] warn: controller logs: %v", stepName, err)
	}

	rc.Logf("[%s] done", stepName)
	return nil
}

// queryAndSave runs a Prometheus range query and writes timestamp,value CSV.
func queryAndSave(ctx context.Context, promURL, query string, start, end time.Time, step, outPath string) error {
	apiURL := fmt.Sprintf("%s/api/v1/query_range", promURL)

	params := url.Values{}
	params.Set("query", query)
	params.Set("start", strconv.FormatInt(start.Unix(), 10))
	params.Set("end", strconv.FormatInt(end.Unix(), 10))
	params.Set("step", step)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL+"?"+params.Encode(), nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("prometheus request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("prometheus status %d: %s", resp.StatusCode, string(body))
	}

	// Parse Prometheus range response.
	var result promRangeResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return fmt.Errorf("parse prometheus response: %w", err)
	}
	if result.Status != "success" {
		return fmt.Errorf("prometheus error: %s", result.Error)
	}

	// Write CSV: timestamp_unix,value
	f, err := os.Create(outPath)
	if err != nil {
		return err
	}
	defer f.Close()

	w := csv.NewWriter(f)
	_ = w.Write([]string{"timestamp", "value"})
	for _, series := range result.Data.Result {
		for _, point := range series.Values {
			if len(point) != 2 {
				continue
			}
			ts, _ := point[0].(json.Number)
			val, _ := point[1].(string)
			_ = w.Write([]string{ts.String(), val})
		}
	}
	w.Flush()
	return w.Error()
}

// promRangeResponse is the top-level structure of /api/v1/query_range.
type promRangeResponse struct {
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
	Data   struct {
		ResultType string `json:"resultType"`
		Result     []struct {
			Metric map[string]string `json:"metric"`
			Values [][]any `json:"values"`
		} `json:"result"`
	} `json:"data"`
}

// ComputeReplicaSeconds reads replicas.csv from resultsDir and returns
// the total replica-seconds: sum(replicas_i) * collectStepSeconds.
// Returns 0 without error if the file doesn't exist yet (best-effort).
func ComputeReplicaSeconds(resultsDir string) (float64, error) {
	path := filepath.Join(resultsDir, "replicas.csv")
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("open replicas.csv: %w", err)
	}
	defer f.Close()

	r := csv.NewReader(f)
	// skip header
	if _, err := r.Read(); err != nil {
		return 0, fmt.Errorf("read replicas.csv header: %w", err)
	}

	var sum float64
	for {
		record, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return 0, fmt.Errorf("read replicas.csv: %w", err)
		}
		if len(record) < 2 {
			continue
		}
		v, err := strconv.ParseFloat(record[1], 64)
		if err != nil {
			continue
		}
		sum += v
	}
	return sum * collectStepSeconds, nil
}

func collectControllerLogsToFile(ctx context.Context, rc *RunContext, clientset kubernetes.Interface, stepName string) error {
	deployName := fmt.Sprintf("scale-controller-%s", rc.RunSpec.ID)

	var pods corev1.PodList
	if err := rc.K8sClient.List(ctx, &pods,
		client.InNamespace(controllerNamespace),
		client.MatchingLabels(map[string]string{"app": deployName}),
	); err != nil {
		return fmt.Errorf("list pods: %w", err)
	}
	if len(pods.Items) == 0 {
		return fmt.Errorf("no controller pod found")
	}

	podName := pods.Items[0].Name
	req := clientset.CoreV1().Pods(controllerNamespace).GetLogs(podName, &corev1.PodLogOptions{})
	stream, err := req.Stream(ctx)
	if err != nil {
		return fmt.Errorf("stream controller logs: %w", err)
	}
	defer stream.Close()

	var buf bytes.Buffer
	if _, err := buf.ReadFrom(stream); err != nil {
		return fmt.Errorf("read controller logs: %w", err)
	}

	outPath := filepath.Join(rc.ResultsDir, "controller.log")
	if err := os.WriteFile(outPath, buf.Bytes(), 0o644); err != nil {
		return fmt.Errorf("write controller.log: %w", err)
	}
	rc.Logf("[%s] saved controller.log (%d bytes)", stepName, buf.Len())
	return nil
}
