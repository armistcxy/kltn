### Kiến trúc hệ thống + Setup

Thành phần:

1. CNPG Cluster
2. Prometheus
3. Load Generator: pgbench, pgedge-loadgen

Set up:

```bash
# Yêu cầu: Change directory tới repo kltn

cd hack/spin-up
NODES=3 CLUSTER_NAME=test make create
make install-all

# Tạo secret chứa mật khẩu ứng dụng
kubectl apply -f db-secret.yaml

# Tạo CNPG cluster (với 2 node khởi tạo)
kubectl apply -f cnpg-cluster.yaml

# Tạo PodMonitor để theo dõi tài nguyên
kubectl apply -f pod-monitor.yaml
```

- db-secret.yaml
    
    ```yaml
    apiVersion: v1  
    data:  
      username: YXBw  # base64 for "app"  
      password: cGFzc3dvcmQ=  # base64 for "app"  
    kind: Secret  
    metadata:
      name: pg-cluster-app-user
    type: kubernetes.io/basic-auth
    ```
    
- cnpg-cluster.yaml
    
    ```yaml
    apiVersion: postgresql.cnpg.io/v1
    kind: Cluster
    metadata:
      name: pg-cluster
    spec:
      instances: 2
      storage:
        size: 10Gi
      bootstrap: 
        initdb: 
          database: app  
          owner: app
          secret:
            name: pg-cluster-app-user
      postgresql:
        parameters:
          max_connections: "200"
          shared_buffers: "256MB"
          wal_level: replica
          pg_stat_statements.max: "10000"
          pg_stat_statements.track: all
    ```
    
- pod-monitor.yaml
    
    ```yaml
    apiVersion: monitoring.coreos.com/v1
    kind: PodMonitor
    metadata:
      name: pg-cluster
      labels:
        release: prometheus
    spec:
      selector:
        matchLabels:
          cnpg.io/cluster: pg-cluster
      podMetricsEndpoints:
      - port: metrics
    ```
    

```bash
# kubectl port-forward -n monitoring svc/prometheus-grafana 3000:80 
# Đăng nhập vào grafana tại localhost:3000/login
# Tài khoản: admin
# Mật khẩu: admin

# Import Grafana dashboard cho cluster: import thông qua ID 20417
```

CONNECTION STRING: `postgres://app:password@localhost:5432/app`

