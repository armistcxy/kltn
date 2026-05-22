package scale

import (
	"context"
	"fmt"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// CNPGClient interacts with CNPG Cluster
type CNPGClient struct {
	k8sClient ctrlclient.Client

	namespace string
	cluster   string
}

func NewCNPGClient(k8sClient ctrlclient.Client, namespace, cluster string) *CNPGClient {
	return &CNPGClient{
		k8sClient: k8sClient,
		namespace: namespace,
		cluster:   cluster,
	}
}

func (c *CNPGClient) GetCurrentInstances(ctx context.Context) (int, error) {
	var cluster cnpgv1.Cluster
	if err := c.k8sClient.Get(ctx, types.NamespacedName{
		Namespace: c.namespace,
		Name:      c.cluster,
	}, &cluster); err != nil {
		return 0, fmt.Errorf("failed to get CNPG Cluster: %w", err)
	}

	return int(cluster.Spec.Instances), nil
}

func (c *CNPGClient) PatchInstances(ctx context.Context, instances int) error {
	var cluster cnpgv1.Cluster
	if err := c.k8sClient.Get(ctx, types.NamespacedName{
		Namespace: c.namespace,
		Name:      c.cluster,
	}, &cluster); err != nil {
		return fmt.Errorf("failed to get CNPG Cluster: %w", err)
	}

	cluster.Spec.Instances = instances

	if err := c.k8sClient.Update(ctx, &cluster); err != nil {
		return fmt.Errorf("failed to update CNPG Cluster instances: %w", err)
	}

	return nil
}
