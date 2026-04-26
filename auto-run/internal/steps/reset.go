package steps

import (
	"context"
	"fmt"
	"time"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	resetTimeout    = 5 * time.Minute
	resetPollPeriod = 10 * time.Second
	cnpgClusterName = "pg-cluster"
	cnpgNamespace   = "default"
)

// ResetCluster patches the CNPG cluster back to 1 instance and waits for
// exactly that many Running pods before returning.
//
// If pods do not converge within resetTimeout, it applies the checklist fixes
// (fix dangling/initializing PVCs, delete stuck recovery jobs) and retries once.
//
// Checklist reference:
//  1. Zero maxSyncReplicas before patching — prevents controller from reverting
//     the patch when instances < maxSyncReplicas+1.
//  2. On timeout: patch dangling PVCs (cnpg.io/pvcStatus=initializing → ready)
//     so the CNPG controller can create the pod and self-heal.
//  3. On timeout: delete stuck recovery jobs (0/1 Completed) so CNPG is unblocked.
func ResetCluster(ctx context.Context, rc *RunContext) error {
	const stepName = "reset-cluster"
	logStep(rc.Log, stepName, "patching CNPG cluster to 1 instance")

	// Step 1 (proactive): zero maxSyncReplicas before patching instances.
	// If maxSyncReplicas > 0 and we patch instances below maxSyncReplicas+1,
	// CNPG silently reverts spec.instances.
	if err := ensureMaxSyncReplicasZero(ctx, rc, stepName); err != nil {
		rc.Logf("[%s] warn: could not zero maxSyncReplicas: %v", stepName, err)
	}

	if err := patchInstances(ctx, rc, 1); err != nil {
		return fmt.Errorf("patch cluster instances: %w", err)
	}
	rc.Logf("[%s] cluster patched → 1 instance; waiting for pods", stepName)

	if err := waitForRunningPods(ctx, rc, stepName, 1, resetTimeout); err == nil {
		rc.Logf("[%s] cluster reset complete", stepName)
		return nil
	}

	// Timeout: apply checklist fixes then retry.
	rc.Logf("[%s] timeout — applying checklist fixes (dangling PVCs, stuck jobs)", stepName)

	if err := fixDanglingPVCs(ctx, rc, stepName); err != nil {
		rc.Logf("[%s] warn: fix dangling PVCs: %v", stepName, err)
	}
	if err := deleteStuckJobs(ctx, rc, stepName); err != nil {
		rc.Logf("[%s] warn: delete stuck jobs: %v", stepName, err)
	}

	rc.Logf("[%s] retrying wait after checklist fixes", stepName)
	if err := waitForRunningPods(ctx, rc, stepName, 1, resetTimeout); err != nil {
		return fmt.Errorf("reset-cluster: still waiting for 1 Running pod after checklist fixes: %w", err)
	}

	rc.Logf("[%s] cluster reset complete (after fixes)", stepName)
	return nil
}

// ensureMaxSyncReplicasZero zeros spec.maxSyncReplicas if it is currently > 0.
// This prevents the CNPG controller from reverting spec.instances when we scale down.
func ensureMaxSyncReplicasZero(ctx context.Context, rc *RunContext, stepName string) error {
	var cluster cnpgv1.Cluster
	if err := rc.K8sClient.Get(ctx, types.NamespacedName{
		Namespace: cnpgNamespace,
		Name:      cnpgClusterName,
	}, &cluster); err != nil {
		return err
	}
	if cluster.Spec.MaxSyncReplicas == 0 {
		return nil
	}
	rc.Logf("[%s] maxSyncReplicas=%d — zeroing to allow scale-down", stepName, cluster.Spec.MaxSyncReplicas)
	patch := client.MergeFrom(cluster.DeepCopy())
	cluster.Spec.MaxSyncReplicas = 0
	return rc.K8sClient.Patch(ctx, &cluster, patch)
}

// patchInstances sets spec.instances on the CNPG cluster.
func patchInstances(ctx context.Context, rc *RunContext, target int) error {
	var cluster cnpgv1.Cluster
	if err := rc.K8sClient.Get(ctx, types.NamespacedName{
		Namespace: cnpgNamespace,
		Name:      cnpgClusterName,
	}, &cluster); err != nil {
		if k8serrors.IsNotFound(err) {
			return fmt.Errorf("CNPG cluster %q not found", cnpgClusterName)
		}
		return fmt.Errorf("get cluster: %w", err)
	}
	patch := client.MergeFrom(cluster.DeepCopy())
	cluster.Spec.Instances = target
	return rc.K8sClient.Patch(ctx, &cluster, patch)
}

// waitForRunningPods polls until exactly target pods with the CNPG cluster label
// are in Running phase, or until timeout/ctx is cancelled.
func waitForRunningPods(ctx context.Context, rc *RunContext, stepName string, target int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		running, err := countRunningPods(ctx, rc.K8sClient)
		if err != nil {
			rc.Logf("[%s] warn: %v", stepName, err)
		} else {
			rc.Logf("[%s] running pods: %d / %d", stepName, running, target)
			if running == target {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(resetPollPeriod):
		}
	}
	return fmt.Errorf("timeout after %s waiting for %d Running pods", timeout, target)
}

// fixDanglingPVCs finds PVCs labelled for this cluster whose pvcStatus annotation
// is still "initializing" and patches them to "ready" so CNPG can proceed.
// This unblocks the deadlock described in step 1 of the reset checklist.
func fixDanglingPVCs(ctx context.Context, rc *RunContext, stepName string) error {
	var pvcList corev1.PersistentVolumeClaimList
	sel := labels.SelectorFromSet(labels.Set{"cnpg.io/cluster": cnpgClusterName})
	if err := rc.K8sClient.List(ctx, &pvcList,
		client.InNamespace(cnpgNamespace),
		client.MatchingLabelsSelector{Selector: sel},
	); err != nil {
		return fmt.Errorf("list PVCs: %w", err)
	}

	for i := range pvcList.Items {
		pvc := &pvcList.Items[i]
		if pvc.Annotations["cnpg.io/pvcStatus"] != "initializing" {
			continue
		}
		rc.Logf("[%s] dangling PVC %s (pvcStatus=initializing) — patching to ready", stepName, pvc.Name)
		patch := client.MergeFrom(pvc.DeepCopy())
		if pvc.Annotations == nil {
			pvc.Annotations = make(map[string]string)
		}
		pvc.Annotations["cnpg.io/pvcStatus"] = "ready"
		if err := rc.K8sClient.Patch(ctx, pvc, patch); err != nil {
			rc.Logf("[%s] warn: patch PVC %s: %v", stepName, pvc.Name, err)
		}
	}
	return nil
}

// deleteStuckJobs removes CNPG recovery jobs that are stuck (not completed).
// A stuck job blocks the cluster from reconciling; deleting it lets CNPG retry.
// This corresponds to step 3 of the reset checklist.
func deleteStuckJobs(ctx context.Context, rc *RunContext, stepName string) error {
	var jobList batchv1.JobList
	sel := labels.SelectorFromSet(labels.Set{"cnpg.io/cluster": cnpgClusterName})
	if err := rc.K8sClient.List(ctx, &jobList,
		client.InNamespace(cnpgNamespace),
		client.MatchingLabelsSelector{Selector: sel},
	); err != nil {
		return fmt.Errorf("list jobs: %w", err)
	}

	for i := range jobList.Items {
		job := &jobList.Items[i]
		if job.Status.Succeeded > 0 || job.Status.CompletionTime != nil {
			continue // completed normally
		}
		rc.Logf("[%s] stuck job %s (succeeded=%d) — deleting", stepName, job.Name, job.Status.Succeeded)
		if err := rc.K8sClient.Delete(ctx, job); err != nil && !k8serrors.IsNotFound(err) {
			rc.Logf("[%s] warn: delete job %s: %v", stepName, job.Name, err)
		}
	}
	return nil
}

func countRunningPods(ctx context.Context, k8s client.Client) (int, error) {
	var pods corev1.PodList
	sel := labels.SelectorFromSet(labels.Set{"cnpg.io/cluster": cnpgClusterName})
	if err := k8s.List(ctx, &pods,
		client.InNamespace(cnpgNamespace),
		client.MatchingLabelsSelector{Selector: sel},
	); err != nil {
		return 0, fmt.Errorf("list pods: %w", err)
	}
	running := 0
	for _, p := range pods.Items {
		if p.Status.Phase == corev1.PodRunning {
			running++
		}
	}
	return running, nil
}
