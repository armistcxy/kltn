# Auto-Run: Benchmark Harness

Auto-Run là hệ thống tự động hoá toàn bộ vòng lặp benchmark, chạy không cần giám sát qua đêm.

```
Web UI (kubectl port-forward)
  ↕  edit matrix / xem progress
Auto-Run Server (in-cluster Pod)
  → reset cluster
  → deploy controller
  → run loadgen
  → collect metrics
  → upload GCS
  → run tiếp...
```

---

## Kiến trúc

### Thành phần

```
auto-run/
├── cmd/server/main.go          # Entrypoint: khởi động orchestrator + HTTP server
├── internal/
│   ├── matrix/
│   │   ├── types.go            # RunSpec, RunState, GlobalDefaults, StepInfo
│   │   └── store.go            # Load matrix.yaml, sync ConfigMap, in-memory state
│   ├── orchestrator/
│   │   └── orchestrator.go     # Vòng lặp chính: chạy run tuần tự, xử lý control signal
│   ├── steps/
│   │   ├── types.go            # RunContext — object truyền qua tất cả bước
│   │   ├── reset.go            # Bước 1: reset CNPG cluster về minInstances
│   │   ├── controller.go       # Bước 2: deploy/teardown scale-controller Deployment
│   │   ├── loadgen.go          # Bước 3: tạo loadgen Job, đợi xong, lấy logs
│   │   ├── collect.go          # Bước 4: query Prometheus → CSV + controller logs
│   │   └── upload.go           # Bước 5: upload results/ lên GCS
│   ├── api/
│   │   └── handler.go          # REST endpoints + SSE log stream
│   ├── bus/
│   │   └── bus.go              # In-memory pub/sub: orchestrator → SSE clients
│   └── filestore/
│       └── store.go            # Quản lý file upload từ Web UI (configs, scenarios)
├── ui/                         # Web UI tĩnh (HTML + vanilla JS)
├── k8s/                        # Kubernetes manifests
├── matrix.yaml                 # Default matrix (danh sách run)
└── Dockerfile
```

### Luồng dữ liệu

```
matrix.yaml ──(load)──► matrix.Store ──(NextQueued)──► Orchestrator
                              │                               │
                         (SetState)                    executeRun()
                              │                               │
                         RunState ◄──(SetStepStatus)──  runStep()
                              │                               │
                         bus.Bus ──(Publish)──► SSE stream ──► Web UI
```

---

## Vòng lặp Orchestrator

File: `auto-run/internal/orchestrator/orchestrator.go`

Orchestrator chạy trong background goroutine, nhận control signal qua channel:

```
[Start] → loop() → NextQueued() → executeRun() → NextQueued() → ...
                                       ↓
                              [Pause / Stop signal]
```

**Control signals:**

| Signal | Hành vi |
|--------|---------|
| `start` | Bắt đầu loop nếu chưa chạy; resume nếu đang pause |
| `pause` | Set flag `pauseAfter = true` → dừng sau khi run hiện tại xong |
| `stop` | Cancel context của run hiện tại + gửi `SignalStop` → thoát loop |
| `retry` | Reset tất cả run về `queued` rồi start lại |

**Session ID** được tạo khi `Start()` được gọi, format `20260102-150405` (UTC), dùng làm prefix GCS.

---

## Năm bước của một run

### Bước 1 — `reset-cluster` (`steps/reset.go`)

**Mục đích:** Đảm bảo cluster bắt đầu ở trạng thái clean (đúng số instance) trước mỗi run.

**Logic:**
1. `GET` CNPG Cluster `pg-cluster` trong namespace `default`
2. Tính `target = cluster.Spec.MinSyncReplicas + 1` (tối thiểu 1)
3. Patch `spec.instances = target`
4. Poll mỗi 10s: đếm pod label `cnpg.io/cluster=pg-cluster` đang `Running`
5. Thành công khi `running == target`

**Timeout:** 5 phút. Quá timeout → run đó `FAILED`.

**Hằng số trong code:**
```go
resetTimeout    = 5 * time.Minute
resetPollPeriod = 10 * time.Second
cnpgClusterName = "pg-cluster"
cnpgNamespace   = "default"
```

---

### Bước 2 — `deploy-controller` (`steps/controller.go`)

**Mục đích:** Deploy đúng config scale-controller cho run này.

**Logic:**
1. Đọc file config YAML (path từ `RunSpec.Config`, relative to `REPO_ROOT`)
2. Tạo ConfigMap `scale-controller-config-<run-id>` chứa `config.yaml`
3. Tạo Deployment `scale-controller-<run-id>` với:
   - Image: `zzzsleepzzz/scale-controller:latest`
   - Args: `--config=/config/config.yaml --prometheus-addr=<url> --namespace=default --db-cluster=pg-cluster --watch-interval=10s --metrics-addr=:9091`
   - Mount ConfigMap vào `/config`
   - `nodeSelector` = `worker_node` nếu được cấu hình
   - Label `auto-run/run-id=<run-id>` để tìm pod khi collect logs
4. Poll `deployment.Status.ReadyReplicas >= 1` mỗi 5s

**Timeout:** 3 phút.

**Teardown** (chạy ngay sau loadgen, kể cả khi failed):
- Xoá Deployment `scale-controller-<run-id>`
- Xoá ConfigMap `scale-controller-config-<run-id>`

**Hằng số trong code:**
```go
controllerImage          = "zzzsleepzzz/scale-controller:latest"
controllerRolloutTimeout = 3 * time.Minute
controllerPollPeriod     = 5 * time.Second
```

---

### Bước 3 — `run-loadgen` (`steps/loadgen.go`)

**Mục đích:** Chạy traffic load theo scenario đã định nghĩa.

**Logic:**
1. Đọc file scenario YAML (path từ `RunSpec.Scenario`, relative to `REPO_ROOT`)
2. Tạo ConfigMap `loadgen-scenario-<run-id>` chứa `scenario.yaml`
3. Xoá Job cũ cùng tên nếu tồn tại (cleanup từ run trước bị interrupt)
4. Tạo Job `loadgen-<run-id>` với:
   - Image: `zzzsleepzzz/loadgen:v2.8`
   - Command: `loadgen run --db-url <db_url> --scenario /scenarios/scenario.yaml --concurrency <n> --workload pgbench-read-heavy`
   - Mount scenario ConfigMap vào `/scenarios`
   - `nodeSelector` = `worker_node` nếu được cấu hình
   - `backoffLimit = 0` (không retry pod)
   - `restartPolicy = Never`
5. Poll Job status mỗi 10s:
   - `succeeded > 0` → thành công, lấy logs
   - `failed > 0` → lấy logs rồi trả lỗi
6. Lưu stdout của Job vào `results/<run-id>/loadgen-summary.txt`
7. Cleanup: xoá Job + scenario ConfigMap

**Timeout:** 40 phút.

**Hằng số trong code:**
```go
loadgenImage      = "zzzsleepzzz/loadgen:v2.8"
loadgenJobTimeout = 40 * time.Minute
loadgenPollPeriod = 10 * time.Second
```

---

### Bước 4 — `collect-metrics` (`steps/collect.go`)

**Mục đích:** Thu thập toàn bộ time-series của run từ Prometheus + logs của controller.

**Logic:**
1. Gọi `GET /api/v1/query_range` với `step=15s` cho từng metric trong window `[startTS, endTS]`
2. Ghi kết quả ra CSV `timestamp,value` trong `results/<run-id>/`
3. Lấy logs pod controller → `results/<run-id>/controller.log`

**Danh sách metrics được thu thập:**

| File CSV | PromQL |
|----------|--------|
| `replicas.csv` | `scaling_instances_current` |
| `replicas_target_final.csv` | `scaling_instances_target_final` |
| `replicas_target_reactive.csv` | `scaling_instances_target_reactive` |
| `replicas_target_predictive.csv` | `scaling_instances_target_predictive` |
| `backends.csv` | `sum(cnpg_backends_total{namespace="default",pod=~"pg-cluster-.*"})` |
| `tps.csv` | `sum(rate(cnpg_pg_stat_database_xact_commit{...}[1m])) + sum(rate(...xact_rollback...))` |
| `metric_raw_backends.csv` | `scaling_observer_metric_value{metric_name="backends"}` |
| `metric_raw_tps.csv` | `scaling_observer_metric_value{metric_name="tps"}` |
| `metric_raw_avg_latency.csv` | `scaling_observer_metric_value{metric_name="avg_latency"}` |

**Lưu ý:** Bước này là best-effort — lỗi ở đây không làm run `FAILED`.

---

### Bước 5 — `upload-gcs` (`steps/upload.go`)

**Mục đích:** Lưu trữ kết quả vĩnh viễn lên GCS để phân tích sau.

**Logic:**
1. Ghi `meta.json` vào `results/<run-id>/`
2. Walk toàn bộ `results/<run-id>/`, upload từng file lên GCS
3. Append dòng vào `runs/<session-id>/index.tsv` trên GCS

**Đường dẫn GCS:**
```
gs://<gcs_bucket>/runs/<session-id>/<run-id>/
  ├── meta.json
  ├── controller.log
  ├── loadgen-summary.txt
  ├── replicas.csv
  ├── replicas_target_final.csv
  ├── replicas_target_reactive.csv
  ├── replicas_target_predictive.csv
  ├── backends.csv
  ├── tps.csv
  ├── metric_raw_backends.csv
  ├── metric_raw_tps.csv
  └── metric_raw_avg_latency.csv
```

**Authentication:** GCS client dùng Application Default Credentials (ADC). Trong GKE, Workload Identity tự động inject credentials — không cần mount JSON key.

**Bước này là best-effort** — lỗi upload không làm run `FAILED`.

**Schema `meta.json`:**
```json
{
  "run_id": "hybrid-gradual-ramp",
  "session_id": "20260406-143000",
  "config_file": "config.example.yaml",
  "scenario_file": "loadgen/scenarios/gradual-ramp.yaml",
  "start_ts": 1743940200,
  "end_ts": 1743941280,
  "duration_s": 1080,
  "git_commit": "b7ec43c",
  "status": "SUCCESS",
  "loadgen_concurrency": 100,
  "worker_node": "gke-pg-autoscale-default-pool-0b3f4459-g364",
  "db_url": "postgres://app:***@pg-cluster-r/app",
  "gcs_path": "gs://my-bucket/runs/20260406-143000/hybrid-gradual-ramp/"
}
```

---

## Cấu hình

### matrix.yaml

File trung tâm kiểm soát toàn bộ benchmark suite. Được load lúc khởi động và sync từ ConfigMap `auto-run-matrix` (để persist qua pod restart).

```yaml
defaults:
  concurrency: 100                                    # số worker loadgen mặc định
  db_url: postgres://app:password@pg-cluster-r/app   # CNPG read endpoint
  prometheus_url: http://prometheus-operated.monitoring:9090
  gcs_bucket: my-thesis-results                       # tên GCS bucket (không có gs://)
  worker_node: ""                                     # hostname node cho nodeSelector; "" = bỏ qua

runs:
  - id: hybrid-gradual-ramp          # unique ID, dùng làm tên folder GCS
    config: config.example.yaml      # đường dẫn config scale-controller, relative to REPO_ROOT
    scenario: loadgen/scenarios/gradual-ramp.yaml   # đường dẫn scenario, relative to REPO_ROOT
    tags: [thesis-eval, hybrid]      # tuỳ chọn, dùng để filter

  - id: predictive-gradual-ramp
    config: scale-controller-config/predictive-only.yaml
    scenario: loadgen/scenarios/gradual-ramp.yaml
    concurrency: 200                 # override defaults.concurrency cho run này
    worker_node: "gke-node-abc123"   # override defaults.worker_node
    tags: [predictive]
```

**Fields của mỗi run:**

| Field | Bắt buộc | Mô tả |
|-------|----------|-------|
| `id` | ✓ | Unique identifier. Dùng làm tên folder GCS và tên Deployment/Job/ConfigMap trong K8s |
| `config` | ✓ | Path đến config.yaml của scale-controller, relative to `REPO_ROOT` |
| `scenario` | ✓ | Path đến scenario YAML của loadgen, relative to `REPO_ROOT` |
| `tags` | | Mảng string tuỳ ý. Dùng để filter run theo nhóm |
| `concurrency` | | Override `defaults.concurrency`. Số worker loadgen |
| `worker_node` | | Override `defaults.worker_node`. Giá trị của label `kubernetes.io/hostname` |

**Fields của `defaults`:**

| Field | Mô tả | Quan trọng |
|-------|-------|-----------|
| `concurrency` | Số worker loadgen nếu run không override | Ảnh hưởng trực tiếp đến tải |
| `db_url` | Connection string đến PostgreSQL | Dùng `pg-cluster-r` (read endpoint) để load qua replica |
| `prometheus_url` | URL Prometheus in-cluster | Dùng để collect metrics sau mỗi run |
| `gcs_bucket` | Tên GCS bucket lưu kết quả | Bỏ trống → bỏ qua bước upload |
| `worker_node` | Hostname node để pin controller + loadgen | Tránh loadgen chạy cùng node với DB pod |

### Node Isolation (worker_node)

Khi `worker_node` được set, cả scale-controller Deployment và loadgen Job đều có:

```yaml
spec:
  template:
    spec:
      nodeSelector:
        kubernetes.io/hostname: "<worker_node>"
```

CNPG Cluster có `nodeAffinity` riêng (trong `infra-init/cnpg-cluster.yaml`) để pin DB pod vào các node DB — không bị ảnh hưởng bởi setting này.

### Environment Variables (server)

Server đọc config từ flag hoặc env var (env var ưu tiên):

| Env Var | Flag | Default | Mô tả |
|---------|------|---------|-------|
| `MATRIX_FILE` | `--matrix` | `/config/matrix.yaml` | Path đến matrix.yaml (thường mount từ ConfigMap) |
| `REPO_ROOT` | `--repo` | `/repo` | Path đến repo root (thường từ init container git clone) |
| `RESULTS_DIR` | `--results` | `/results` | Thư mục staging local trước khi upload GCS |
| `UPLOADS_DIR` | `--uploads` | `/uploads` | Thư mục lưu file upload từ Web UI |
| `UI_DIR` | `--ui` | `/ui` | Thư mục chứa file Web UI tĩnh |

---

## Kubernetes Manifests

### RBAC (`k8s/rbac.yaml`)

Server cần ServiceAccount `auto-run-ksa` với quyền:
- `get/list/watch/patch` — CNPG Cluster CRD (`postgresql.cnpg.io/clusters`)
- `create/get/list/watch/delete` — Deployments, Jobs, ConfigMaps, Pods (namespace `default`)
- `get/list` — Nodes (validate worker_node)

### Deployment (`k8s/deployment.yaml`)

```
Init container: git-clone (alpine/git)
  └── git clone https://${GITHUB_TOKEN}@github.com/... /repo
      (GITHUB_TOKEN từ Secret auto-run-secrets)

Container: auto-run (zzzsleepzzz/auto-run:latest)
  ├── Volumes:
  │     matrix-config  → /config   (ConfigMap auto-run-matrix)
  │     repo           → /repo     (emptyDir, populated bởi init container)
  │     results        → /results  (emptyDir)
  │     uploads        → /uploads  (emptyDir)
  └── Resources: 100m/128Mi request, 500m/512Mi limit
```

**Lưu ý:** Không cần `GOOGLE_APPLICATION_CREDENTIALS` — Workload Identity của GKE inject credentials tự động qua metadata server.

### Secret cần tạo thủ công

```bash
kubectl create secret generic auto-run-secrets \
  --from-literal=github-token=<PAT> \
  --from-literal=gcs_bucket=<bucket-name>   # tuỳ chọn, có thể cấu hình qua Web UI
```

---

## REST API

Server lắng nghe trên `:8080`. Truy cập từ local:

```bash
kubectl port-forward svc/auto-run 8080:8080
```

| Method | Path | Mô tả |
|--------|------|-------|
| `GET` | `/api/matrix` | Trả về toàn bộ matrix + trạng thái runtime mỗi run |
| `PUT` | `/api/matrix` | Thay thế toàn bộ matrix (lưu vào ConfigMap) |
| `PATCH` | `/api/runs/:id` | Chỉnh sửa một run (config, scenario, tags, concurrency) |
| `POST` | `/api/runs/:id/move` | Reorder (`{"after": "run-id"}`) |
| `DELETE` | `/api/runs/:id` | Xoá run (chỉ khi status `queued`) |
| `POST` | `/api/control` | `{"action": "start"\|"pause"\|"stop"\|"retry"}` |
| `GET` | `/api/runs/:id/logs` | SSE stream log real-time |
| `GET` | `/api/settings` | Trả về `defaults` |
| `PUT` | `/api/settings` | Cập nhật `defaults` (lưu vào ConfigMap) |
| `GET` | `/api/status` | Health check (dùng bởi readinessProbe) |

---

## Setup & Deploy

```bash
# 1. Build + push image
docker build -f auto-run/Dockerfile -t zzzsleepzzz/auto-run:latest .
docker push zzzsleepzzz/auto-run:latest

# 2. Tạo Secret
kubectl create secret generic auto-run-secrets \
  --from-literal=github-token=<GITHUB_PAT>

# 3. Setup Workload Identity (thay YOUR_PROJECT_ID)
gcloud iam service-accounts add-iam-policy-binding \
  auto-run-gsa@YOUR_PROJECT_ID.iam.gserviceaccount.com \
  --role roles/iam.workloadIdentityUser \
  --member "serviceAccount:YOUR_PROJECT_ID.svc.id.goog[default/auto-run-ksa]"

gcloud projects add-iam-policy-binding YOUR_PROJECT_ID \
  --member "serviceAccount:auto-run-gsa@YOUR_PROJECT_ID.iam.gserviceaccount.com" \
  --role roles/storage.objectAdmin

# Cập nhật annotation trong k8s/rbac.yaml:
# iam.gke.io/gcp-service-account: auto-run-gsa@YOUR_PROJECT_ID.iam.gserviceaccount.com

# 4. Deploy
kubectl apply -f auto-run/k8s/

# 5. Truy cập Web UI
kubectl port-forward svc/auto-run 8080:8080
# → http://localhost:8080

# 6. Port-forward Prometheus (khi debug local)
kubectl port-forward -n monitoring svc/prometheus-operated 9090:9090
```

---

## Timeouts & Thông số quan trọng

| Hằng số | Giá trị | Ý nghĩa |
|---------|---------|---------|
| `resetTimeout` | 5 phút | Thời gian tối đa chờ cluster về minInstances |
| `resetPollPeriod` | 10s | Chu kỳ kiểm tra pod count khi reset |
| `controllerRolloutTimeout` | 3 phút | Thời gian tối đa chờ controller Deployment ready |
| `controllerPollPeriod` | 5s | Chu kỳ kiểm tra controller rollout |
| `loadgenJobTimeout` | 40 phút | Thời gian tối đa chờ loadgen Job hoàn thành |
| `loadgenPollPeriod` | 10s | Chu kỳ poll trạng thái loadgen Job |
| Prometheus step | 15s | Độ phân giải time-series khi collect metrics |

---

## Ghi chú thiết kế

**Teardown luôn chạy:** Dù loadgen thành công hay thất bại, `TeardownController` đều được gọi để dọn Deployment + ConfigMap, tránh conflict với run tiếp theo.

**Upload là best-effort:** Nếu GCS upload thất bại, run vẫn được đánh dấu `SUCCESS`/`FAILED` theo kết quả của loadgen — kết quả local vẫn còn trong `results/<run-id>/`.

**GCS bucket bỏ trống:** Nếu `defaults.gcs_bucket = ""`, bước upload bị bỏ qua hoàn toàn — tiện khi test locally.

**Persist qua pod restart:** Matrix state được sync từ ConfigMap `auto-run-matrix` lúc khởi động. File upload từ Web UI được persist qua ConfigMap trong `filestore`. Tuy nhiên, `results/` và `uploads/` dùng `emptyDir` nên mất khi pod restart.
