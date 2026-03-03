# PostgreSQL Intelligent Auto-Scaling on Kubernetes

A thesis project that implements database-aware auto-scaling for PostgreSQL using Kubernetes, moving beyond traditional CPU-based metrics to scale based on actual database load (active connections, query latency) and predictive forecasting.

## Thesis Contribution

**Problem**: Most default Kubernetes scalers rely on CPU/Memory metrics. However, databases often bottleneck on locking, IOPS, or connection limits before CPU hits 100%.

**Solution**: Build a system that scales PostgreSQL Read Replicas based on real database load metrics and compare it against standard CPU scaling.

### Key Innovation
- **Reactive Scaling**: Scale based on current database load (active connections, query latency)
- **Predictive Scaling**: Use time-series forecasting (Holt-Winters, Facebook Prophet) to anticipate load spikes
- **Cost-Aware Scheduling**: Hybrid topology with On-Demand Primary and Spot Instance Replicas
- **Self-Healing**: Automatic recovery from infrastructure failures (simulated via Chaos Engineering)