package compute

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"
)

// TestCreateAndDeleteInstance tests creating and deleting a VM instance
func TestCreateAndDeleteInstance(t *testing.T) {
	t.Parallel()
	if os.Getenv("GCP_PROJECT_ID") == "" {
		t.Skip("GCP_PROJECT_ID not set, skipping integration test")
	}

	projectID := os.Getenv("GCP_PROJECT_ID")
	ctx := context.Background()

	// Create client
	client, err := NewGCPVMClient(ctx, projectID)
	if err != nil {
		t.Fatalf("Failed to create GCPVMClient: %v", err)
	}

	// Test instance configuration
	instance := &VMInstance{
		Name:         "test-vm-create-" + fmt.Sprintf("%d", time.Now().Unix()),
		Zone:         "us-central1-a",
		MachineType:  "zones/us-central1-a/machineTypes/e2-micro",
		Image:        "projects/debian-cloud/global/images/family/debian-12",
		BootDiskSize: 10,
		Tags:         []string{"test-vm"},
		Labels:       map[string]string{"test": "true"},
	}

	// Create instance
	createCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	err = client.Create(createCtx, instance)
	if err != nil {
		t.Errorf("Failed to create instance: %v", err)
		return
	}

	t.Logf("Successfully created instance: %s", instance.Name)

	// Ensure cleanup
	t.Cleanup(func() {
		deleteCtx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()

		err := client.Delete(deleteCtx, instance.Name, instance.Zone)
		if err != nil {
			t.Logf("Warning: Failed to cleanup instance %s: %v", instance.Name, err)
		} else {
			t.Logf("Successfully cleaned up instance: %s", instance.Name)
		}
	})
}

// TestGetInstance tests retrieving a VM instance
func TestGetInstance(t *testing.T) {
	t.Parallel()
	if os.Getenv("GCP_PROJECT_ID") == "" {
		t.Skip("GCP_PROJECT_ID not set, skipping integration test")
	}

	projectID := os.Getenv("GCP_PROJECT_ID")
	ctx := context.Background()

	// Create client
	client, err := NewGCPVMClient(ctx, projectID)
	if err != nil {
		t.Fatalf("Failed to create GCPVMClient: %v", err)
	}

	// Create a test instance first
	instance := &VMInstance{
		Name:         "test-vm-get-" + fmt.Sprintf("%d", time.Now().Unix()),
		Zone:         "us-central1-a",
		MachineType:  "zones/us-central1-a/machineTypes/e2-micro",
		Image:        "projects/debian-cloud/global/images/family/debian-12",
		BootDiskSize: 10,
	}

	createCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	err = client.Create(createCtx, instance)
	if err != nil {
		t.Fatalf("Failed to create test instance: %v", err)
	}

	// Cleanup after test
	t.Cleanup(func() {
		deleteCtx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		client.Delete(deleteCtx, instance.Name, instance.Zone)
	})

	// Wait a moment for instance to be created
	time.Sleep(2 * time.Second)

	// Get the instance
	getCtx, cancel := context.WithTimeout(ctx, 1*time.Minute)
	defer cancel()

	retrieved, err := client.Get(getCtx, instance.Name, instance.Zone)
	if err != nil {
		t.Errorf("Failed to get instance: %v", err)
		return
	}

	if retrieved == nil {
		t.Error("Retrieved instance is nil")
		return
	}

	if retrieved.Name != instance.Name {
		t.Errorf("Instance name mismatch: got %s, want %s", retrieved.Name, instance.Name)
	}

	if retrieved.Zone != instance.Zone {
		t.Errorf("Instance zone mismatch: got %s, want %s", retrieved.Zone, instance.Zone)
	}

	t.Logf("Successfully retrieved instance: %s", retrieved.Name)
}

// TestListInstances tests listing VM instances in a zone
func TestListInstances(t *testing.T) {
	t.Parallel()
	if os.Getenv("GCP_PROJECT_ID") == "" {
		t.Skip("GCP_PROJECT_ID not set, skipping integration test")
	}

	projectID := os.Getenv("GCP_PROJECT_ID")
	ctx := context.Background()

	// Create client
	client, err := NewGCPVMClient(ctx, projectID)
	if err != nil {
		t.Fatalf("Failed to create GCPVMClient: %v", err)
	}

	// Create test instances
	instances := []*VMInstance{
		{
			Name:         "test-vm-list-1-" + fmt.Sprintf("%d", time.Now().Unix()),
			Zone:         "us-central1-a",
			MachineType:  "zones/us-central1-a/machineTypes/e2-micro",
			Image:        "projects/debian-cloud/global/images/family/debian-12",
			BootDiskSize: 10,
		},
		{
			Name:         "test-vm-list-2-" + fmt.Sprintf("%d", time.Now().Unix()),
			Zone:         "us-central1-a",
			MachineType:  "zones/us-central1-a/machineTypes/e2-micro",
			Image:        "projects/debian-cloud/global/images/family/debian-12",
			BootDiskSize: 10,
		},
	}

	// Create instances
	for _, inst := range instances {
		createCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
		err := client.Create(createCtx, inst)
		cancel()
		if err != nil {
			t.Logf("Failed to create test instance %s: %v", inst.Name, err)
			continue
		}

		// Cleanup after test
		t.Cleanup(func() {
			deleteCtx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			defer cancel()
			client.Delete(deleteCtx, inst.Name, inst.Zone)
		})
	}

	// Wait for instances to be created
	time.Sleep(2 * time.Second)

	// List instances
	listCtx, cancel := context.WithTimeout(ctx, 1*time.Minute)
	defer cancel()

	retrieved, err := client.List(listCtx, "us-central1-a")
	if err != nil {
		t.Errorf("Failed to list instances: %v", err)
		return
	}

	if retrieved == nil {
		t.Error("Retrieved instance list is nil")
		return
	}

	t.Logf("Successfully listed %d instances", len(retrieved))

	// Verify our test instances are in the list
	found := 0
	for _, inst := range instances {
		for _, listed := range retrieved {
			if listed.Name == inst.Name {
				found++
				break
			}
		}
	}

	t.Logf("Found %d of %d created test instances", found, len(instances))
}

// TestStartAndStopInstance tests starting and stopping a VM instance
func TestStartAndStopInstance(t *testing.T) {
	t.Parallel()
	if os.Getenv("GCP_PROJECT_ID") == "" {
		t.Skip("GCP_PROJECT_ID not set, skipping integration test")
	}

	projectID := os.Getenv("GCP_PROJECT_ID")
	ctx := context.Background()

	// Create client
	client, err := NewGCPVMClient(ctx, projectID)
	if err != nil {
		t.Fatalf("Failed to create GCPVMClient: %v", err)
	}

	// Create test instance
	instance := &VMInstance{
		Name:         "test-vm-start-stop-" + fmt.Sprintf("%d", time.Now().Unix()),
		Zone:         "us-central1-a",
		MachineType:  "zones/us-central1-a/machineTypes/e2-micro",
		Image:        "projects/debian-cloud/global/images/family/debian-12",
		BootDiskSize: 10,
	}

	createCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	err = client.Create(createCtx, instance)
	cancel()
	if err != nil {
		t.Fatalf("Failed to create test instance: %v", err)
	}

	// Cleanup after test
	t.Cleanup(func() {
		deleteCtx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		client.Delete(deleteCtx, instance.Name, instance.Zone)
	})

	// Wait for instance to be created
	time.Sleep(2 * time.Second)

	// Stop the instance
	stopCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	err = client.Stop(stopCtx, instance.Name, instance.Zone)
	if err != nil {
		t.Errorf("Failed to stop instance: %v", err)
	} else {
		t.Logf("Successfully stopped instance: %s", instance.Name)
	}

	// Wait a moment
	time.Sleep(2 * time.Second)

	// Start the instance
	startCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	err = client.Start(startCtx, instance.Name, instance.Zone)
	if err != nil {
		t.Errorf("Failed to start instance: %v", err)
	} else {
		t.Logf("Successfully started instance: %s", instance.Name)
	}
}

// TestInstanceWithPreemptibleFlag tests creating a preemptible instance
func TestInstanceWithPreemptibleFlag(t *testing.T) {
	t.Parallel()
	if os.Getenv("GCP_PROJECT_ID") == "" {
		t.Skip("GCP_PROJECT_ID not set, skipping integration test")
	}

	projectID := os.Getenv("GCP_PROJECT_ID")
	ctx := context.Background()

	// Create client
	client, err := NewGCPVMClient(ctx, projectID)
	if err != nil {
		t.Fatalf("Failed to create GCPVMClient: %v", err)
	}

	// Create preemptible instance
	instance := &VMInstance{
		Name:            "test-vm-preemptible-" + fmt.Sprintf("%d", time.Now().Unix()),
		Zone:            "us-central1-a",
		MachineType:     "zones/us-central1-a/machineTypes/e2-micro",
		Image:           "projects/debian-cloud/global/images/family/debian-12",
		BootDiskSize:    10,
		PreemptibleFlag: true,
		Tags:            []string{"preemptible"},
		Labels:          map[string]string{"preemptible": "true"},
	}

	createCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	err = client.Create(createCtx, instance)
	if err != nil {
		t.Errorf("Failed to create preemptible instance: %v", err)
		return
	}

	t.Logf("Successfully created preemptible instance: %s", instance.Name)

	// Cleanup after test
	t.Cleanup(func() {
		deleteCtx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		client.Delete(deleteCtx, instance.Name, instance.Zone)
	})

	// Verify the instance was created with preemptible flag
	time.Sleep(2 * time.Second)

	getCtx, cancel := context.WithTimeout(ctx, 1*time.Minute)
	defer cancel()

	retrieved, err := client.Get(getCtx, instance.Name, instance.Zone)
	if err != nil {
		t.Errorf("Failed to get instance: %v", err)
		return
	}

	if retrieved.PreemptibleFlag != instance.PreemptibleFlag {
		t.Errorf("Preemptible flag mismatch: got %v, want %v", retrieved.PreemptibleFlag, instance.PreemptibleFlag)
	}

	t.Logf("Verified preemptible flag is set correctly")
}
