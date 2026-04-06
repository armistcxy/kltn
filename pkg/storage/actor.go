package storage

import (
	"context"
	"fmt"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// Actor applies storage resize decisions by patching the CNPG Cluster CR.
//
// It never patches PVCs directly , all changes go through spec.storage.size
// and spec.walStorage.size so CNPG can reconcile safely.
type Actor struct {
	k8sClient ctrlclient.Client
	namespace string
	cluster   string
}

// NewActor creates an Actor targeting the given CNPG cluster.
func NewActor(k8sClient ctrlclient.Client, namespace, cluster string) *Actor {
	return &Actor{
		k8sClient: k8sClient,
		namespace: namespace,
		cluster:   cluster,
	}
}

// ResizePGData patches spec.storage.size on the CNPG Cluster CR.
//
// newSize must be a valid Kubernetes resource quantity string (e.g. "15Gi").
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

// ResizeWAL patches spec.walStorage.size on the CNPG Cluster CR.
//
// Returns an error if the cluster has no dedicated walStorage configured.
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
