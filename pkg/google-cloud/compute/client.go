package compute

import (
	"context"
	"fmt"
	"strings"

	compute "cloud.google.com/go/compute/apiv1"
	"cloud.google.com/go/compute/apiv1/computepb"
	"google.golang.org/api/option"
)

// GCPVMClient implements the VMInstanceManager interface
type GCPVMClient struct {
	projectID string
	client    *compute.InstancesClient
}

// NewGCPVMClient creates a new GCP VM instance client
func NewGCPVMClient(ctx context.Context, projectID string, opts ...option.ClientOption) (VMInstanceManager, error) {
	client, err := compute.NewInstancesRESTClient(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create compute client: %w", err)
	}

	return &GCPVMClient{
		projectID: projectID,
		client:    client,
	}, nil
}

// Create creates a new VM instance
func (c *GCPVMClient) Create(ctx context.Context, instance *VMInstance) error {
	if err := validateVMInstance(instance); err != nil {
		return fmt.Errorf("invalid VM instance: %w", err)
	}

	// Build the instance request
	instanceResource := &computepb.Instance{
		Name:        &instance.Name,
		Zone:        &instance.Zone,
		MachineType: &instance.MachineType,
		Tags: &computepb.Tags{
			Items: instance.Tags,
		},
		Labels: instance.Labels,
		Disks: []*computepb.AttachedDisk{
			{
				Boot:       boolPtr(true),
				AutoDelete: boolPtr(true),
				InitializeParams: &computepb.AttachedDiskInitializeParams{
					SourceImage: &instance.Image,
					DiskSizeGb:  &instance.BootDiskSize,
				},
			},
		},
	}

	// If preemptible is true, VM will cheaper but can be terminated anytime
	instanceResource.Scheduling = &computepb.Scheduling{
		Preemptible: &instance.PreemptibleFlag,
	}

	// Add service account if provided
	if instance.ServiceAccount != "" {
		instanceResource.ServiceAccounts = []*computepb.ServiceAccount{
			{
				Email: &instance.ServiceAccount,
				Scopes: []string{
					"https://www.googleapis.com/auth/devstorage.read_only",
					"https://www.googleapis.com/auth/logging.write",
					"https://www.googleapis.com/auth/monitoring.write",
				},
			},
		}
	}

	// Add startup script if provided
	if instance.Startup != "" {
		instanceResource.Metadata = &computepb.Metadata{
			Items: []*computepb.Items{
				{
					Key:   strPtr("startup-script"),
					Value: &instance.Startup,
				},
			},
		}
	}

	// Add network interface (required by GCP)
	network := instance.Network
	if network == "" {
		network = fmt.Sprintf("projects/%s/global/networks/default", c.projectID)
	} else if !isNetworkURL(network) {
		// If network is not a full URL, construct it
		network = fmt.Sprintf("projects/%s/global/networks/%s", c.projectID, network)
	}

	subnetwork := instance.Subnet
	if subnetwork != "" && !isSubnetworkURL(subnetwork) {
		// If subnet is not a full URL, construct it
		zone := instance.Zone
		subnetwork = fmt.Sprintf("projects/%s/regions/%s/subnetworks/%s", c.projectID, extractRegion(zone), subnetwork)
	}

	networkInterface := &computepb.NetworkInterface{
		Network: &network,
	}

	if subnetwork != "" {
		networkInterface.Subnetwork = &subnetwork
	}

	instanceResource.NetworkInterfaces = []*computepb.NetworkInterface{networkInterface}

	// Execute create request
	op, err := c.client.Insert(ctx, &computepb.InsertInstanceRequest{
		Project:          c.projectID,
		Zone:             instance.Zone,
		InstanceResource: instanceResource,
	})
	if err != nil {
		return fmt.Errorf("failed to create instance: %w", err)
	}

	// Wait for operation to complete
	if err := op.Wait(ctx); err != nil {
		return fmt.Errorf("failed waiting for operation: %w", err)
	}

	return nil
}

// Delete deletes a VM instance
func (c *GCPVMClient) Delete(ctx context.Context, name, zone string) error {
	op, err := c.client.Delete(ctx, &computepb.DeleteInstanceRequest{
		Project:  c.projectID,
		Zone:     zone,
		Instance: name,
	})
	if err != nil {
		return fmt.Errorf("failed to delete instance: %w", err)
	}

	if err := op.Wait(ctx); err != nil {
		return fmt.Errorf("failed waiting for operation: %w", err)
	}

	return nil
}

// Get retrieves a VM instance by name and zone
func (c *GCPVMClient) Get(ctx context.Context, name, zone string) (*VMInstance, error) {
	instance, err := c.client.Get(ctx, &computepb.GetInstanceRequest{
		Project:  c.projectID,
		Zone:     zone,
		Instance: name,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get instance: %w", err)
	}

	preemptible := false
	if instance.Scheduling != nil && instance.Scheduling.Preemptible != nil {
		preemptible = *instance.Scheduling.Preemptible
	}

	return &VMInstance{
		Name:            *instance.Name,
		Zone:            zone,
		MachineType:     *instance.MachineType,
		PreemptibleFlag: preemptible,
	}, nil
}

// Start starts a stopped VM instance
func (c *GCPVMClient) Start(ctx context.Context, name, zone string) error {
	op, err := c.client.Start(ctx, &computepb.StartInstanceRequest{
		Project:  c.projectID,
		Zone:     zone,
		Instance: name,
	})
	if err != nil {
		return fmt.Errorf("failed to start instance: %w", err)
	}

	if err := op.Wait(ctx); err != nil {
		return fmt.Errorf("failed waiting for operation: %w", err)
	}

	return nil
}

// Stop stops a running VM instance
func (c *GCPVMClient) Stop(ctx context.Context, name, zone string) error {
	op, err := c.client.Stop(ctx, &computepb.StopInstanceRequest{
		Project:  c.projectID,
		Zone:     zone,
		Instance: name,
	})
	if err != nil {
		return fmt.Errorf("failed to stop instance: %w", err)
	}

	if err := op.Wait(ctx); err != nil {
		return fmt.Errorf("failed waiting for operation: %w", err)
	}

	return nil
}

// List lists all VM instances in a zone
func (c *GCPVMClient) List(ctx context.Context, zone string) ([]*VMInstance, error) {
	var instances []*VMInstance

	it := c.client.List(ctx, &computepb.ListInstancesRequest{
		Project: c.projectID,
		Zone:    zone,
	})

	for {
		instance, err := it.Next()
		if err != nil {
			break
		}

		preemptible := false
		if instance.Scheduling != nil && instance.Scheduling.Preemptible != nil {
			preemptible = *instance.Scheduling.Preemptible
		}

		instances = append(instances, &VMInstance{
			Name:            *instance.Name,
			Zone:            zone,
			MachineType:     *instance.MachineType,
			PreemptibleFlag: preemptible,
		})
	}

	return instances, nil
}

// Helper functions

func validateVMInstance(instance *VMInstance) error {
	if instance.Name == "" {
		return fmt.Errorf("instance name is required")
	}
	if instance.Zone == "" {
		return fmt.Errorf("zone is required")
	}
	if instance.MachineType == "" {
		return fmt.Errorf("machine type is required")
	}
	if instance.Image == "" {
		return fmt.Errorf("image is required")
	}
	return nil
}

func boolPtr(b bool) *bool {
	return &b
}

func strPtr(s string) *string {
	return &s
}

// isNetworkURL checks if the network string is already a full URL
func isNetworkURL(network string) bool {
	return len(network) > 0 && (network[0] == '/' || (len(network) > 7 && network[:8] == "projects"))
}

// isSubnetworkURL checks if the subnetwork string is already a full URL
func isSubnetworkURL(subnetwork string) bool {
	return len(subnetwork) > 0 && (subnetwork[0] == '/' || (len(subnetwork) > 7 && subnetwork[:8] == "projects"))
}

// extractRegion extracts the region from a zone name (e.g., "us-central1-a" -> "us-central1")
func extractRegion(zone string) string {
	parts := strings.Split(zone, "-")
	if len(parts) >= 2 {
		return strings.Join(parts[:len(parts)-1], "-")
	}
	return zone
}
