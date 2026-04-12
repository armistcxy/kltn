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

---

### init-volume-snapshot

#### 1. Enable GCE Persistent Disk CSI driver

```bash
gcloud container clusters update auto-scaling-lab --update-addons=GcePersistentDiskCsiDriver=ENABLED --region us-east1
```

#### 2. Cài VolumeSnapshot CRDs

GKE không cài sẵn VolumeSnapshot CRDs nên phải tự cài:

```bash
kubectl apply -f https://raw.githubusercontent.com/kubernetes-csi/external-snapshotter/master/client/config/crd/snapshot.storage.k8s.io_volumesnapshotclasses.yaml

kubectl apply -f https://raw.githubusercontent.com/kubernetes-csi/external-snapshotter/master/client/config/crd/snapshot.storage.k8s.io_volumesnapshotcontents.yaml

kubectl apply -f https://raw.githubusercontent.com/kubernetes-csi/external-snapshotter/master/client/config/crd/snapshot.storage.k8s.io_volumesnapshots.yaml
```

#### 3. Cài snapshot controller

```bash
kubectl apply -f https://raw.githubusercontent.com/kubernetes-csi/external-snapshotter/master/deploy/kubernetes/snapshot-controller/rbac-snapshot-controller.yaml

kubectl apply -f https://raw.githubusercontent.com/kubernetes-csi/external-snapshotter/master/deploy/kubernetes/snapshot-controller/setup-snapshot-controller.yaml
```

#### 4. Tạo VolumeSnapshotClass cho GKE

```bash
kubectl apply -f volumesnapshotclass.yaml
```

#### 5. Tạo GCS Bucket

```bash
PROJECT_ID=$(gcloud config get-value project)
BUCKET_NAME="cnpg-wal-archive-${PROJECT_ID}"
REGION="us-east1"

gcloud storage buckets create gs://${BUCKET_NAME} \
  --location=${REGION} \
  --uniform-bucket-level-access

gcloud storage buckets describe gs://${BUCKET_NAME}
```

#### 6. Tạo Google Service Account (GSA)

```bash
GSA_NAME="cnpg-backup-sa"

gcloud iam service-accounts create ${GSA_NAME} \
  --display-name="CNPG Backup Service Account"

gcloud storage buckets add-iam-policy-binding gs://${BUCKET_NAME} \
  --member="serviceAccount:${GSA_NAME}@${PROJECT_ID}.iam.gserviceaccount.com" \
  --role="roles/storage.objectAdmin"
```

#### 7. Tạo Kubernetes Service Account (KSA)

```bash
NAMESPACE="default"
KSA_NAME="cnpg-backup-ksa"

kubectl create serviceaccount ${KSA_NAME} -n ${NAMESPACE}
```

#### 8. Bind GSA với KSA (Workload Identity)

```bash
# Kiểm tra cluster đã enable Workload Identity chưa
gcloud container clusters describe auto-scaling-lab \
  --region=${REGION} \
  --format="value(workloadIdentityConfig)"

# Nếu chưa enable:
gcloud container clusters update auto-scaling-lab \
  --region=${REGION} \
  --workload-pool=${PROJECT_ID}.svc.id.goog

# Enable trên node pool
gcloud container node-pools update default-pool \
  --cluster=auto-scaling-lab \
  --region=${REGION} \
  --workload-metadata=GKE_METADATA

# Allow KSA impersonate GSA
gcloud iam service-accounts add-iam-policy-binding \
  ${GSA_NAME}@${PROJECT_ID}.iam.gserviceaccount.com \
  --role="roles/iam.workloadIdentityUser" \
  --member="serviceAccount:${PROJECT_ID}.svc.id.goog[${NAMESPACE}/${KSA_NAME}]"

# Annotate KSA với GSA email
kubectl annotate serviceaccount ${KSA_NAME} \
  -n ${NAMESPACE} \
  iam.gke.io/gcp-service-account=${GSA_NAME}@${PROJECT_ID}.iam.gserviceaccount.com
```

#### 9. Cấu hình CNPG Cluster với VolumeSnapshot backup

```bash
# Cài cert-manager (barman cloud plugin cần)
kubectl apply -f https://github.com/cert-manager/cert-manager/releases/latest/download/cert-manager.yaml

# Cài barman cloud plugin
kubectl apply -f https://github.com/cloudnative-pg/plugin-barman-cloud/releases/download/v0.11.0/manifest.yaml

# Tạo ObjectStore
kubectl apply -f objectstore.yaml

# Tạo ScheduledBackup
kubectl apply -f backup.yaml
```

