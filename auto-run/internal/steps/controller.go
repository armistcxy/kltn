package steps

import (
	"context"
	"fmt"
	"os"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	controllerNamespace     = "default"
	controllerImage         = "zzzsleepzzz/scale-controller:latest"
	controllerRolloutTimeout = 3 * time.Minute
	controllerPollPeriod    = 5 * time.Second
)

// DeployController creates a ConfigMap with the run's config.yaml and a
// Deployment of the scale-controller, then waits for rollout.
func DeployController(ctx context.Context, rc *RunContext) error {
	const stepName = "deploy-controller"

	configData, err := os.ReadFile(rc.ConfigPath())
	if err != nil {
		return fmt.Errorf("[%s] read config file %s: %w", stepName, rc.ConfigPath(), err)
	}

	cmName := fmt.Sprintf("scale-controller-config-%s", rc.RunSpec.ID)
	deployName := fmt.Sprintf("scale-controller-%s", rc.RunSpec.ID)

	// --- ConfigMap ---
	rc.Logf("[%s] applying ConfigMap %s", stepName, cmName)
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cmName,
			Namespace: controllerNamespace,
			Labels:    runLabels(rc.RunSpec.ID),
		},
		Data: map[string]string{"config.yaml": string(configData)},
	}
	if err := applyConfigMap(ctx, rc.K8sClient, cm); err != nil {
		return fmt.Errorf("[%s] apply ConfigMap: %w", stepName, err)
	}

	// --- Deployment ---
	rc.Logf("[%s] applying Deployment %s", stepName, deployName)
	deploy := buildControllerDeployment(deployName, cmName, rc)
	if err := applyDeployment(ctx, rc.K8sClient, deploy); err != nil {
		return fmt.Errorf("[%s] apply Deployment: %w", stepName, err)
	}

	// --- PodMonitor (so Prometheus scrapes controller metrics) ---
	pm := buildControllerPodMonitor(rc.RunSpec.ID)
	if err := rc.K8sClient.Create(ctx, pm); err != nil && !k8serrors.IsAlreadyExists(err) {
		rc.Logf("[%s] warn: create PodMonitor: %v", stepName, err)
	} else {
		rc.Logf("[%s] PodMonitor %s created", stepName, pm.GetName())
	}

	// --- Wait for rollout ---
	rc.Logf("[%s] waiting for rollout (timeout %s)", stepName, controllerRolloutTimeout)
	deadline := time.Now().Add(controllerRolloutTimeout)
	for time.Now().Before(deadline) {
		var d appsv1.Deployment
		key := client.ObjectKey{Namespace: controllerNamespace, Name: deployName}
		if err := rc.K8sClient.Get(ctx, key, &d); err != nil {
			return fmt.Errorf("[%s] get deployment: %w", stepName, err)
		}
		if d.Status.ReadyReplicas >= 1 {
			rc.Logf("[%s] controller ready", stepName)
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(controllerPollPeriod):
		}
	}
	return fmt.Errorf("[%s] timeout waiting for controller rollout", stepName)
}

// TeardownController deletes the controller Deployment and ConfigMap for a run.
func TeardownController(ctx context.Context, rc *RunContext) {
	const stepName = "teardown-controller"
	cmName := fmt.Sprintf("scale-controller-config-%s", rc.RunSpec.ID)
	deployName := fmt.Sprintf("scale-controller-%s", rc.RunSpec.ID)

	var deploy appsv1.Deployment
	key := client.ObjectKey{Namespace: controllerNamespace, Name: deployName}
	if err := rc.K8sClient.Get(ctx, key, &deploy); err == nil {
		if err := rc.K8sClient.Delete(ctx, &deploy); err != nil {
			rc.Logf("[%s] warn: delete deployment: %v", stepName, err)
		}
	}

	var cm corev1.ConfigMap
	cmKey := client.ObjectKey{Namespace: controllerNamespace, Name: cmName}
	if err := rc.K8sClient.Get(ctx, cmKey, &cm); err == nil {
		if err := rc.K8sClient.Delete(ctx, &cm); err != nil {
			rc.Logf("[%s] warn: delete configmap: %v", stepName, err)
		}
	}
	pm := buildControllerPodMonitor(rc.RunSpec.ID)
	if err := rc.K8sClient.Delete(ctx, pm); err != nil && !k8serrors.IsNotFound(err) {
		rc.Logf("[%s] warn: delete PodMonitor: %v", stepName, err)
	}

	rc.Logf("[%s] controller resources deleted", stepName)
}

// CollectControllerLogs fetches all logs from the controller pod and returns them.
func CollectControllerLogs(ctx context.Context, rc *RunContext) (string, error) {
	deployName := fmt.Sprintf("scale-controller-%s", rc.RunSpec.ID)

	var pods corev1.PodList
	if err := rc.K8sClient.List(ctx, &pods,
		client.InNamespace(controllerNamespace),
		client.MatchingLabels(map[string]string{
			"auto-run/run-id": rc.RunSpec.ID,
			"app":             deployName,
		}),
	); err != nil {
		return "", fmt.Errorf("list controller pods: %w", err)
	}
	if len(pods.Items) == 0 {
		return "", fmt.Errorf("no controller pod found for run %s", rc.RunSpec.ID)
	}
	// Return pod name - actual log retrieval uses REST client (see collect.go).
	return pods.Items[0].Name, nil
}

func buildControllerDeployment(name, cmName string, rc *RunContext) *appsv1.Deployment {
	replicas := int32(1)
	workerNode := rc.EffectiveWorkerNode()

	deploy := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: controllerNamespace,
			Labels:    runLabels(rc.RunSpec.ID),
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app":             name,
					"auto-run/run-id": rc.RunSpec.ID,
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app":             name,
						"auto-run/run-id": rc.RunSpec.ID,
					},
				},
				Spec: corev1.PodSpec{
					ServiceAccountName: "scale-controller",
					Containers: []corev1.Container{
						{
							Name:  "controller",
							Image: controllerImage,
							Args: []string{
								"--config=/config/config.yaml",
								fmt.Sprintf("--prometheus-addr=%s", rc.PrometheusURL),
								fmt.Sprintf("--namespace=%s", cnpgNamespace),
								fmt.Sprintf("--db-cluster=%s", cnpgClusterName),
								"--watch-interval=10s",
								"--metrics-addr=:9091",
								"--log-file=/dev/null",
							},
							Ports: []corev1.ContainerPort{
								{ContainerPort: 9091, Name: "metrics"},
							},
							VolumeMounts: []corev1.VolumeMount{
								{Name: "config", MountPath: "/config"},
							},
						},
					},
					Volumes: []corev1.Volume{
						{
							Name: "config",
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
		deploy.Spec.Template.Spec.NodeSelector = map[string]string{
			"kubernetes.io/hostname": workerNode,
		}
	}
	return deploy
}

var podMonitorGVK = schema.GroupVersionKind{
	Group:   "monitoring.coreos.com",
	Version: "v1",
	Kind:    "PodMonitor",
}

func buildControllerPodMonitor(runID string) *unstructured.Unstructured {
	name := fmt.Sprintf("scale-controller-%s", runID)
	u := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "monitoring.coreos.com/v1",
			"kind":       "PodMonitor",
			"metadata": map[string]interface{}{
				"name":      name,
				"namespace": controllerNamespace,
				"labels": map[string]interface{}{
					"release":         "prometheus",
					"auto-run/run-id": runID,
					"managed-by":      "auto-run",
				},
			},
			"spec": map[string]interface{}{
				"selector": map[string]interface{}{
					"matchLabels": map[string]interface{}{
						"app":             name,
						"auto-run/run-id": runID,
					},
				},
				"podMetricsEndpoints": []interface{}{
					map[string]interface{}{"port": "metrics"},
				},
			},
		},
	}
	u.SetGroupVersionKind(podMonitorGVK)
	return u
}

func runLabels(runID string) map[string]string {
	return map[string]string{
		"auto-run/run-id": runID,
		"managed-by":      "auto-run",
	}
}

func applyConfigMap(ctx context.Context, k8s client.Client, cm *corev1.ConfigMap) error {
	var existing corev1.ConfigMap
	key := client.ObjectKey{Namespace: cm.Namespace, Name: cm.Name}
	if err := k8s.Get(ctx, key, &existing); err != nil {
		if k8serrors.IsNotFound(err) {
			return k8s.Create(ctx, cm)
		}
		return err
	}
	existing.Data = cm.Data
	existing.Labels = cm.Labels
	return k8s.Update(ctx, &existing)
}

func applyDeployment(ctx context.Context, k8s client.Client, d *appsv1.Deployment) error {
	var existing appsv1.Deployment
	key := client.ObjectKey{Namespace: d.Namespace, Name: d.Name}
	if err := k8s.Get(ctx, key, &existing); err != nil {
		if k8serrors.IsNotFound(err) {
			return k8s.Create(ctx, d)
		}
		return err
	}
	existing.Spec = d.Spec
	existing.Labels = d.Labels
	return k8s.Update(ctx, &existing)
}

