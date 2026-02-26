---

### Kiến trúc hệ thống + Setup

Thành phần:

1. CNPG Cluster
2. Prometheus
3. KEDA
4. Load Generator: pgbench, pgedge-loadgen

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

# Port-forward rw (master node)
kubectl port-forward svc/pg-cluster-rw 5432
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

Chuẩn bị load sẽ thử nghiệm thông qua một tool là `pgedge-loadgen`

```bash
# Initialize database with 5GB of wholesale data
pgedge-loadgen init \
    --app wholesale \
    --size 5GB \
    --connection "postgres://app:password@localhost:5432/app"

# Run load simulation with 50 connections
pgedge-loadgen run \
    --app wholesale \
    --connections 50 \
    --profile local-office \
    --connection "postgres://app:password@localhost:5432/app"
```

Configuration có thể được tìm thông qua Mục `Configuration` trong CNPG Dashboard

---

### Các bước thực hiện

- Thử nghiệm scale dựa trên CPU Usage và Memory Usage
    
    **Trước nhất Enable metrics API**
    
    ```bash
    kubectl apply -f https://github.com/kubernetes-sigs/metrics-server/releases/latest/download/components.yaml
    # Cần thêm tham số `--kubelet-insecure-tls` để metrics server bỏ qua việc kiểm chứng
    # chỉ bảo mật của Node (vì đây là môi trường lab)
    kubectl patch deployment metrics-server -n kube-system --type='json' -p='[{"op": "add", "path": "/spec/template/spec/containers/0/args/-", "value": "--kubelet-insecure-tls"}]'
    ```
    
    **Tạo ScaledObject với threshold CPU: 60%**
    
    - pg-keda-cpu.yaml
        
        ```yaml
        apiVersion: keda.sh/v1alpha1
        kind: ScaledObject
        metadata:
          name: pg-cluster-scaler
          namespace: default
        spec:
          scaleTargetRef:
            apiVersion: postgresql.cnpg.io/v1
            kind: Cluster
            name: pg-cluster
          minReplicaCount: 2
          maxReplicaCount: 10
          triggers:
          - type: cpu
            metricType: Utilization
            metadata:
              value: "60"
        ```
        
    
    ```bash
    kubectl apply -f pg-keda-cpu.yaml
    ```
    
    **Theo dõi**
    
    ```bash
    # Theo dõi KEDA ScaledObject
    kubectl get scaledobject -w
    
    # Theo dõi trạng thái Cluster của CNPG
    kubectl cnpg status pg-cluster -v
    
    # Theo dõi tài nguyên pod thực tế
    kubectl top pods -l cnpg.io/cluster=pg-cluster
    ```
    
    Qua thử nghiệm thì mình thấy rất ảo vì không thể tính được CPU hiện tại của cluster
    
    ![image.png](attachment:237be8ec-0038-4c75-9d02-edf1120b69e3:image.png)
    
    ***Mình sẽ skip cách này***
    
- Thử nghiệm với metric liên quan đến TPS
    
    `cnpg_pg_stat_database_xact_commit` : Tổng số transaction đã commit thành công (có thể dùng hàm irate() để tính TPS)
    
    `cnpg_pg_stat_database_xact_rollback` : Tổng số transaction đã bị rollback
    
    query cái này mỗi 15s để quyết định
    
    ```bash
    query: |
      sum(rate(cnpg_pg_stat_database_xact_commit{namespace="default", pod=~"pg-cluster-.*"}[1m])) 
      + 
      sum(rate(cnpg_pg_stat_database_xact_rollback{namespace="default", pod=~"pg-cluster-.*"}[1m]))
    ```
    

Vấn đề hiện tại gặp phải: HPA dùng selector để tìm pod thuộc target, tính toán metric nhưng HPA đang phải scale một CRD mà scale subresource không có selector

Tuy nhiên CloudNativePG không expose status.selector trong scale subresource.

⇒ Buộc phải dừng lại lab này vì không khả thi

⇒ Phải tự code lại một Controller
