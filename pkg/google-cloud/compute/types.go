package compute

import "context"

// VMInstance represents a Google Compute Engine VM instance configuration
type VMInstance struct {
	Name            string
	Zone            string
	MachineType     string
	Image           string
	Startup         string
	ServiceAccount  string
	Tags            []string
	Labels          map[string]string
	BootDiskSize    int64 // in GB
	PreemptibleFlag bool
	Network         string // Network for the instance (default: "default")
	Subnet          string // Subnet for the instance (optional)
}

// VMInstanceManager defines the interface for VM instance operations
type VMInstanceManager interface {
	Create(ctx context.Context, instance *VMInstance) error
	Delete(ctx context.Context, name, zone string) error
	Get(ctx context.Context, name, zone string) (*VMInstance, error)
	Start(ctx context.Context, name, zone string) error
	Stop(ctx context.Context, name, zone string) error
	List(ctx context.Context, zone string) ([]*VMInstance, error)
}
