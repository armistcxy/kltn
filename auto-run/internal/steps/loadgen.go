package steps

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	loadgenImage      = "zzzsleepzzz/loadgen:v2.12"
	loadgenNamespace  = "default"
	loadgenJobTimeout = 40 * time.Minute
	loadgenPollPeriod = 10 * time.Second
)

// RunLoadgen creates a K8s Job that runs the loadgen binary with the scenario
// file, waits for completion, collects stdout, and saves it to results dir.
// It returns when the Job finishes (success or failure).
func RunLoadgen(ctx context.Context, rc *RunContext, clientset kubernetes.Interface) error {
	const stepName = "run-loadgen"

	scenarioData, err := os.ReadFile(rc.ScenarioPath())
	if err != nil {
		return fmt.Errorf("[%s] read scenario %s: %w", stepName, rc.ScenarioPath(), err)
	}

	cmName := fmt.Sprintf("loadgen-scenario-%s", rc.RunSpec.ID)
	jobName := fmt.Sprintf("loadgen-%s", rc.RunSpec.ID)

	// --- ConfigMap for scenario file ---
	rc.Logf("[%s] creating scenario ConfigMap %s", stepName, cmName)
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cmName,
			Namespace: loadgenNamespace,
			Labels:    runLabels(rc.RunSpec.ID),
		},
		Data: map[string]string{"scenario.yaml": string(scenarioData)},
	}
	if err := applyConfigMap(ctx, rc.K8sClient, cm); err != nil {
		return fmt.Errorf("[%s] apply scenario ConfigMap: %w", stepName, err)
	}

	// --- Delete existing job if any (from a previous failed run) ---
	_ = deleteJob(ctx, rc.K8sClient, jobName)

	// --- Create Job ---
	rc.Logf("[%s] creating loadgen Job %s (concurrency=%d, scale-factor=%d)", stepName, jobName, rc.EffectiveConcurrency(), rc.EffectiveScaleFactor())
	job := buildLoadgenJob(jobName, cmName, rc)
	if err := rc.K8sClient.Create(ctx, job); err != nil {
		return fmt.Errorf("[%s] create Job: %w", stepName, err)
	}

	// --- Wait for Job completion ---
	deadline := time.Now().Add(loadgenJobTimeout)
	var finalJob batchv1.Job
	for time.Now().Before(deadline) {
		key := client.ObjectKey{Namespace: loadgenNamespace, Name: jobName}
		if err := rc.K8sClient.Get(ctx, key, &finalJob); err != nil {
			return fmt.Errorf("[%s] get job: %w", stepName, err)
		}
		if finalJob.Status.Succeeded > 0 {
			rc.Logf("[%s] job succeeded", stepName)
			break
		}
		if finalJob.Status.Failed > 0 {
			// Collect logs before returning error.
			_ = collectJobLogs(ctx, rc, clientset, jobName, stepName)
			return fmt.Errorf("[%s] loadgen job failed (failed pods: %d)", stepName, finalJob.Status.Failed)
		}
		rc.Logf("[%s] job running… (active=%d)", stepName, finalJob.Status.Active)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(loadgenPollPeriod):
		}
	}

	// --- Collect stdout → results dir ---
	if err := collectJobLogs(ctx, rc, clientset, jobName, stepName); err != nil {
		rc.Logf("[%s] warn: collect logs: %v", stepName, err)
	}

	// --- Cleanup ---
	_ = deleteJob(ctx, rc.K8sClient, jobName)
	var delCM corev1.ConfigMap
	cmKey := client.ObjectKey{Namespace: loadgenNamespace, Name: cmName}
	if err := rc.K8sClient.Get(ctx, cmKey, &delCM); err == nil {
		_ = rc.K8sClient.Delete(ctx, &delCM)
	}

	if finalJob.Status.Succeeded == 0 {
		return fmt.Errorf("[%s] timeout after %s", stepName, loadgenJobTimeout)
	}
	return nil
}

// buildLoadgenCommand assembles the loadgen CLI invocation for this run.
func buildLoadgenCommand(rc *RunContext) []string {
	cmd := []string{
		"loadgen", "run",
		"--db-url", rc.Defaults.DBURL,
		"--scenario", "/scenarios/scenario.yaml",
		"--concurrency", fmt.Sprintf("%d", rc.EffectiveConcurrency()),
		"--workload", "pgbench-read-heavy",
	}
	if sf := rc.EffectiveScaleFactor(); sf > 0 {
		cmd = append(cmd, "--scale-factor", fmt.Sprintf("%d", sf))
	}
	return cmd
}

func buildLoadgenJob(name, cmName string, rc *RunContext) *batchv1.Job {
	backoffLimit := int32(0)
	workerNode := rc.EffectiveWorkerNode()

	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: loadgenNamespace,
			Labels:    runLabels(rc.RunSpec.ID),
		},
		Spec: batchv1.JobSpec{
			BackoffLimit: &backoffLimit,
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: runLabels(rc.RunSpec.ID),
				},
				Spec: corev1.PodSpec{
					RestartPolicy: corev1.RestartPolicyNever,
					Containers: []corev1.Container{
						{
							Name:  "loadgen",
							Image: loadgenImage,
							Command: buildLoadgenCommand(rc),
							VolumeMounts: []corev1.VolumeMount{
								{Name: "scenario", MountPath: "/scenarios"},
							},
						},
					},
					Volumes: []corev1.Volume{
						{
							Name: "scenario",
							VolumeSource: corev1.VolumeSource{
								ConfigMap: &corev1.ConfigMapVolumeSource{
									LocalObjectReference: corev1.LocalObjectReference{Name: cmName},
								},
							},
						},
					},
				},
			},
		},
	}

	if workerNode != "" {
		job.Spec.Template.Spec.NodeSelector = map[string]string{
			"kubernetes.io/hostname": workerNode,
		}
	}
	return job
}

func collectJobLogs(ctx context.Context, rc *RunContext, clientset kubernetes.Interface, jobName, stepName string) error {
	// Find the pod for this job.
	var pods corev1.PodList
	if err := rc.K8sClient.List(ctx, &pods,
		client.InNamespace(loadgenNamespace),
		client.MatchingLabels(runLabels(rc.RunSpec.ID)),
	); err != nil {
		return fmt.Errorf("list pods: %w", err)
	}
	if len(pods.Items) == 0 {
		return fmt.Errorf("no pod found for job %s", jobName)
	}
	podName := pods.Items[0].Name

	req := clientset.CoreV1().Pods(loadgenNamespace).GetLogs(podName, &corev1.PodLogOptions{})
	stream, err := req.Stream(ctx)
	if err != nil {
		return fmt.Errorf("stream logs: %w", err)
	}
	defer stream.Close()

	var buf bytes.Buffer
	if _, err := buf.ReadFrom(stream); err != nil {
		return fmt.Errorf("read logs: %w", err)
	}

	outPath := filepath.Join(rc.ResultsDir, "loadgen-summary.txt")
	if err := os.WriteFile(outPath, buf.Bytes(), 0o644); err != nil {
		return fmt.Errorf("write loadgen-summary.txt: %w", err)
	}
	rc.Logf("[%s] saved loadgen output → %s (%d bytes)", stepName, outPath, buf.Len())
	return nil
}

func deleteJob(ctx context.Context, k8s client.Client, name string) error {
	var job batchv1.Job
	key := client.ObjectKey{Namespace: loadgenNamespace, Name: name}
	if err := k8s.Get(ctx, key, &job); err != nil {
		if k8serrors.IsNotFound(err) {
			return nil
		}
		return err
	}
	prop := metav1.DeletePropagationForeground
	return k8s.Delete(ctx, &job, &client.DeleteOptions{
		PropagationPolicy: &prop,
	})
}
