package workload

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Workload is executed repeatedly by each worker goroutine
type Workload interface {
	Name() string
	Execute(ctx context.Context, conn *pgxpool.Conn) error
}

var registry = map[string]Workload{}

// Register makes a workload available by name
func Register(w Workload) {
	registry[w.Name()] = w
}

func Get(name string) (Workload, error) {
	w, ok := registry[name]
	if !ok {
		return nil, fmt.Errorf("unknown workload %q (registered: %v)", name, Names())
	}
	return w, nil
}

func Names() []string {
	names := make([]string, 0, len(registry))
	for n := range registry {
		names = append(names, n)
	}
	return names
}
