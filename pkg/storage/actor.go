package storage

import (
	"context"
	"fmt"
	"time"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// Actor applies storage resize decisions by patching the CNPG Cluster CR
type Actor struct {
	k8sClient ctrlclient.Client
	namespace string
	cluster   string
}

func NewActor(k8sClient ctrlclient.Client, namespace, cluster string) *Actor {
	return &Actor{
		k8sClient: k8sClient,
		namespace: namespace,
		cluster:   cluster,
	}
}

// ResizePGData patches spec.storage.size on the CNPG Cluster CR.
func (a *Actor) ResizePGData(ctx context.Context, newSize string) error {
	var cl cnpgv1.Cluster
	if err := a.k8sClient.Get(ctx, types.NamespacedName{
		Namespace: a.namespace,
		Name:      a.cluster,
	}, &cl); err != nil {
		return fmt.Errorf("get cluster: %w", err)
	}

	cl.Spec.StorageConfiguration.Size = newSize

	if err := a.k8sClient.Update(ctx, &cl); err != nil {
		return fmt.Errorf("update cluster storage size: %w", err)
	}

	return nil
}

func (a *Actor) WaitForPVCExpansion(ctx context.Context, role string, targetSize string) (time.Duration, error) {
	target, err := resource.ParseQuantity(targetSize)
	if err != nil {
		return 0, fmt.Errorf("parse target size %q: %w", targetSize, err)
	}

	sel := labels.SelectorFromSet(labels.Set{
		"cnpg.io/cluster": a.cluster,
		"cnpg.io/pvcRole": role,
	})

	start := time.Now()
	interval := 2 * time.Second
	const maxInterval = 30 * time.Second

	for {
		var pvcList corev1.PersistentVolumeClaimList
		if err := a.k8sClient.List(ctx, &pvcList,
			ctrlclient.InNamespace(a.namespace),
			ctrlclient.MatchingLabelsSelector{Selector: sel},
		); err != nil {
			return 0, fmt.Errorf("list PVCs: %w", err)
		}

		if len(pvcList.Items) > 0 {
			allExpanded := true
			for _, pvc := range pvcList.Items {
				actual, ok := pvc.Status.Capacity[corev1.ResourceStorage]
				if !ok || actual.Cmp(target) < 0 {
					allExpanded = false
					break
				}
			}
			if allExpanded {
				return time.Since(start), nil
			}
		}

		select {
		case <-ctx.Done():
			return 0, fmt.Errorf("timeout waiting for %s PVC expansion to %s: %w", role, targetSize, ctx.Err())
		case <-time.After(interval):
		}

		interval *= 2
		if interval > maxInterval {
			interval = maxInterval
		}
	}
}

// ResizeWAL patches spec.walStorage.size on the CNPG Cluster CR.
func (a *Actor) ResizeWAL(ctx context.Context, newSize string) error {
	var cl cnpgv1.Cluster
	if err := a.k8sClient.Get(ctx, types.NamespacedName{
		Namespace: a.namespace,
		Name:      a.cluster,
	}, &cl); err != nil {
		return fmt.Errorf("get cluster: %w", err)
	}

	if cl.Spec.WalStorage == nil {
		return fmt.Errorf("cluster %q has no spec.walStorage, cannot resize WAL volume", a.cluster)
	}

	cl.Spec.WalStorage.Size = newSize

	if err := a.k8sClient.Update(ctx, &cl); err != nil {
		return fmt.Errorf("update cluster walStorage size: %w", err)
	}

	return nil
}
