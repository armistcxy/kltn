package scale

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/clientcmd"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

var (
	k8sClientOnce sync.Once
	k8sClient     ctrlclient.Client
)

func TestGetCurrentInstances(t *testing.T) {
	client := createK8sClient(t)
	cnpgClient := NewCNPGClient(client, "default", "pg-cluster")

	ctx, cancel := context.WithTimeout(context.Background(), time.Second*10)
	defer cancel()

	instances, err := cnpgClient.GetCurrentInstances(ctx)
	if err != nil {
		t.Fatalf("failed to get current instances: %v", err)
	}

	t.Logf("Current instances: %d", instances)
}

func TestPatchInstances(t *testing.T) {
	client := createK8sClient(t)
	cnpgClient := NewCNPGClient(client, "default", "pg-cluster")

	ctx, cancel := context.WithTimeout(context.Background(), time.Second*120)
	defer cancel()

	// Get current instances
	currentInstances, err := cnpgClient.GetCurrentInstances(ctx)
	if err != nil {
		t.Fatalf("failed to get current instances: %v", err)
	}

	// Patch to currentInstances + 1
	newInstances := currentInstances + 1
	if err := cnpgClient.PatchInstances(ctx, newInstances); err != nil {
		t.Fatalf("failed to patch instances: %v", err)
	}

	t.Logf("Patched instances to: %d", newInstances)

	// Wait a bit for the update to take effect
	time.Sleep(time.Second * 15)

	// Check if patched correctly
	updatedInstances, err := cnpgClient.GetCurrentInstances(ctx)
	if err != nil {
		t.Fatalf("failed to get updated instances: %v", err)
	}
	if updatedInstances != newInstances {
		t.Fatalf("expected instances %d, got %d", newInstances, updatedInstances)
	}

	t.Logf("Verified patched instances: %d", updatedInstances)

	// Revert back to original instances
	if err := cnpgClient.PatchInstances(ctx, currentInstances); err != nil {
		t.Fatalf("failed to revert instances: %v", err)
	}
}

func createK8sClient(t *testing.T) ctrlclient.Client {
	k8sClientOnce.Do(func() {
		kubeconfig := os.Getenv("KUBECONFIG")
		if kubeconfig == "" {
			home, _ := os.UserHomeDir()
			kubeconfig = fmt.Sprintf("%s/.kube/config", home)
		}

		scheme := runtime.NewScheme()

		utilruntime.Must(clientgoscheme.AddToScheme(scheme))
		utilruntime.Must(cnpgv1.AddToScheme(scheme))

		cfg, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
		if err != nil {
			t.Fatalf("failed to build kubeconfig: %v", err)
		}

		k8sClient, err = ctrlclient.New(cfg, ctrlclient.Options{
			Scheme: scheme,
		})
		if err != nil {
			t.Fatalf("failed to create k8s client: %v", err)
		}
	})

	return k8sClient
}
