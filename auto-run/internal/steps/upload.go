package steps

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"cloud.google.com/go/storage"
	"google.golang.org/api/iterator"
)

// Meta is written as meta.json alongside the benchmark artefacts.
type Meta struct {
	RunID          string `json:"run_id"`
	SessionID      string `json:"session_id"`
	ConfigFile     string `json:"config_file"`
	ScenarioFile   string `json:"scenario_file"`
	StartTS        int64  `json:"start_ts"`
	EndTS          int64  `json:"end_ts"`
	DurationS      int64  `json:"duration_s"`
	GitCommit      string `json:"git_commit"`
	Status         string `json:"status"`
	Concurrency    int    `json:"loadgen_concurrency"`
	WorkerNode     string `json:"worker_node"`
	DBURL          string  `json:"db_url"` // password redacted
	GCSPath        string  `json:"gcs_path"`
	ReplicaSeconds float64 `json:"replica_seconds"`
}

// WriteMeta serialises meta to <resultsDir>/meta.json.
func WriteMeta(resultsDir string, m Meta) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(resultsDir, "meta.json"), data, 0o644)
}

// UploadGCS uploads all files in resultsDir to
// gs://<bucket>/runs/<sessionID>/<runID>/ and returns the GCS path.
func UploadGCS(ctx context.Context, rc *RunContext, sessionID string) (string, error) {
	const stepName = "upload-gcs"

	bucket := rc.EffectiveGCSBucket()
	if bucket == "" {
		return "", fmt.Errorf("[%s] GCS_BUCKET not configured", stepName)
	}

	prefix := fmt.Sprintf("runs/%s/%s/", sessionID, rc.RunSpec.ID)
	gcsPath := fmt.Sprintf("gs://%s/%s", bucket, prefix)
	rc.Logf("[%s] uploading %s → %s", stepName, rc.ResultsDir, gcsPath)

	// Build GCS client. Uses GOOGLE_APPLICATION_CREDENTIALS env or workload identity.
	// google.golang.org/api/option.WithCredentialsFile is deprecated; rely on
	// Application Default Credentials (ADC) picked up automatically by the SDK
	// when GOOGLE_APPLICATION_CREDENTIALS is set in the environment.
	gcsClient, err := storage.NewClient(ctx)
	if err != nil {
		return "", fmt.Errorf("[%s] create GCS client: %w", stepName, err)
	}
	defer gcsClient.Close()

	bkt := gcsClient.Bucket(bucket)

	// Walk results dir and upload each file.
	if err := filepath.Walk(rc.ResultsDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(rc.ResultsDir, path)
		objName := prefix + strings.ReplaceAll(rel, string(filepath.Separator), "/")

		f, err := os.Open(path)
		if err != nil {
			return fmt.Errorf("open %s: %w", path, err)
		}
		defer f.Close()

		obj := bkt.Object(objName)
		w := obj.NewWriter(ctx)
		if _, err := io.Copy(w, f); err != nil {
			_ = w.Close()
			return fmt.Errorf("upload %s: %w", objName, err)
		}
		if err := w.Close(); err != nil {
			return fmt.Errorf("close writer %s: %w", objName, err)
		}
		rc.Logf("[%s] uploaded %s", stepName, objName)
		return nil
	}); err != nil {
		return "", fmt.Errorf("[%s] walk results: %w", stepName, err)
	}

	// Append to index.tsv in the session root.
	if err := appendIndex(ctx, bkt, sessionID, rc.RunSpec.ID, gcsPath); err != nil {
		rc.Logf("[%s] warn: update index.tsv: %v", stepName, err)
	}

	rc.Logf("[%s] upload complete → %s", stepName, gcsPath)
	return gcsPath, nil
}

func appendIndex(ctx context.Context, bkt *storage.BucketHandle, sessionID, runID, gcsPath string) error {
	indexObj := bkt.Object(fmt.Sprintf("runs/%s/index.tsv", sessionID))

	// Read existing content (may not exist yet).
	var existing string
	r, err := indexObj.NewReader(ctx)
	if err == nil {
		b, _ := io.ReadAll(r)
		r.Close()
		existing = string(b)
	} else if !isNotFound(err) {
		return err
	}

	ts := time.Now().UTC().Format(time.RFC3339)
	line := fmt.Sprintf("%s\t%s\tSUCCESS\t%s\n", runID, gcsPath, ts)
	if existing == "" {
		existing = "run_id\tgcs_path\tstatus\ttimestamp\n"
	}

	w := indexObj.NewWriter(ctx)
	if _, err := io.WriteString(w, existing+line); err != nil {
		_ = w.Close()
		return err
	}
	return w.Close()
}

func isNotFound(err error) bool {
	return err == storage.ErrObjectNotExist ||
		strings.Contains(err.Error(), "object doesn't exist") ||
		err == iterator.Done
}
