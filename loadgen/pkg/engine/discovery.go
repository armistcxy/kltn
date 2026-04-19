package engine

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// DiscoveryPool resolves a headless-service DNS hostname to individual pod IPs
// and maintains one pgxpool per pod. Workers use AcquireForWorker so each worker
// is pinned to a specific pod (workerIdx % podCount). When pod count changes on
// re-discovery the modulo automatically redistributes workers to the new pod.
// Re-discovery runs every DiscoverInterval so the pool tracks autoscaling events.
type DiscoveryPool struct {
	baseURL          string
	concPerPod       int
	discoverInterval time.Duration

	mu   sync.RWMutex
	pods []*podPool
}

type podPool struct {
	ip   string
	pool *pgxpool.Pool
}

// NewDiscoveryPool creates a DiscoveryPool. Call Start() before use.
func NewDiscoveryPool(baseURL string, concPerPod int, discoverInterval time.Duration) *DiscoveryPool {
	return &DiscoveryPool{
		baseURL:          baseURL,
		concPerPod:       concPerPod,
		discoverInterval: discoverInterval,
	}
}

// Start performs the initial DNS resolution and opens per-pod pools.
// Launches a background goroutine to re-discover on each interval.
func (dp *DiscoveryPool) Start(ctx context.Context) error {
	if err := dp.discover(ctx); err != nil {
		return err
	}
	go func() {
		t := time.NewTicker(dp.discoverInterval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				_ = dp.discover(ctx)
			}
		}
	}()
	return nil
}

// AcquireForWorker returns a connection from the pod assigned to this worker.
// Assignment is workerIdx % podCount, so the mapping is stable while pod count
// is unchanged and naturally redistributes to new pods when count changes.
func (dp *DiscoveryPool) AcquireForWorker(ctx context.Context, workerIdx int) (*pgxpool.Conn, error) {
	dp.mu.RLock()
	pods := dp.pods
	dp.mu.RUnlock()

	if len(pods) == 0 {
		return nil, fmt.Errorf("discovery: no backend pods available")
	}
	return pods[workerIdx%len(pods)].pool.Acquire(ctx)
}

// Acquire satisfies connPool for warmup (uses worker 0's pod — warmup calls
// AcquireForWorker directly with the correct index instead).
func (dp *DiscoveryPool) Acquire(ctx context.Context) (*pgxpool.Conn, error) {
	return dp.AcquireForWorker(ctx, 0)
}

// AnyPool returns one of the underlying pools (used for one-time Prepare calls).
func (dp *DiscoveryPool) AnyPool() *pgxpool.Pool {
	dp.mu.RLock()
	defer dp.mu.RUnlock()
	if len(dp.pods) == 0 {
		return nil
	}
	return dp.pods[0].pool
}

// PodCount returns the number of currently discovered pods.
func (dp *DiscoveryPool) PodCount() int {
	dp.mu.RLock()
	defer dp.mu.RUnlock()
	return len(dp.pods)
}

// Close closes all per-pod pools.
func (dp *DiscoveryPool) Close() {
	dp.mu.Lock()
	defer dp.mu.Unlock()
	for _, p := range dp.pods {
		p.pool.Close()
	}
	dp.pods = nil
}

func (dp *DiscoveryPool) discover(ctx context.Context) error {
	ips, err := resolveAllIPs(dp.baseURL)
	if err != nil {
		return err
	}

	dp.mu.Lock()
	defer dp.mu.Unlock()

	existing := make(map[string]*pgxpool.Pool, len(dp.pods))
	for _, p := range dp.pods {
		existing[p.ip] = p.pool
	}

	newPods := make([]*podPool, 0, len(ips))
	seen := make(map[string]bool, len(ips))
	for _, ip := range ips {
		seen[ip] = true
		if pool, ok := existing[ip]; ok {
			newPods = append(newPods, &podPool{ip: ip, pool: pool})
		} else {
			pool, err := buildPodPool(ctx, dp.baseURL, ip, dp.concPerPod)
			if err != nil {
				continue // skip unreachable pod, keep trying next interval
			}
			newPods = append(newPods, &podPool{ip: ip, pool: pool})
		}
	}

	// Close pools for pods that are no longer in DNS.
	for ip, pool := range existing {
		if !seen[ip] {
			pool.Close()
		}
	}

	dp.pods = newPods
	return nil
}

// resolveAllIPs does a DNS A-record lookup for the hostname in rawURL.
// With a headless Kubernetes service this returns one IP per pod.
func resolveAllIPs(rawURL string) ([]string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}
	host := u.Hostname()
	addrs, err := net.LookupHost(host)
	if err != nil {
		return nil, fmt.Errorf("DNS lookup %q: %w", host, err)
	}
	return addrs, nil
}

// buildPodPool opens a pgxpool directly to podIP, substituting it into baseURL.
func buildPodPool(ctx context.Context, baseURL, podIP string, concPerPod int) (*pgxpool.Pool, error) {
	u, err := url.Parse(baseURL)
	if err != nil {
		return nil, err
	}
	if port := u.Port(); port != "" {
		u.Host = net.JoinHostPort(podIP, port)
	} else {
		u.Host = podIP
	}

	cfg, err := pgxpool.ParseConfig(u.String())
	if err != nil {
		return nil, err
	}
	cfg.MaxConns = int32(concPerPod + 2)
	cfg.MinConns = 0
	cfg.MaxConnLifetime = 30 * time.Second
	cfg.MaxConnIdleTime = 10 * time.Second

	return pgxpool.NewWithConfig(ctx, cfg)
}
