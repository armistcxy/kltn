// Package workload defines the Workload interface and the built-in workloads.
package workload

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Workload is executed repeatedly by each worker goroutine.
// Implementations must be safe for concurrent use.
type Workload interface {
	Name() string
	Execute(ctx context.Context, pool *pgxpool.Pool) error
}

var registry = map[string]Workload{}

// Register makes a workload available by name.
func Register(w Workload) {
	registry[w.Name()] = w
}

// Get returns a registered workload by name.
func Get(name string) (Workload, error) {
	w, ok := registry[name]
	if !ok {
		return nil, fmt.Errorf("unknown workload %q (registered: %v)", name, Names())
	}
	return w, nil
}

// Names returns all registered workload names.
func Names() []string {
	names := make([]string, 0, len(registry))
	for n := range registry {
		names = append(names, n)
	}
	return names
}
