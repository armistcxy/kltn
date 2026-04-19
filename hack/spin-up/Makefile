TARGET        ?= local# local | gke
CLUSTER_NAME  ?= auto-scaling-lab
NODES         ?= 1

# GKE only
PROJECT_ID    ?= $(shell gcloud config get-value project 2>/dev/null)
ZONE          ?= us-east1-b
MACHINE_TYPE  ?= e2-standard-2

# Sub-pool (quota workaround)
SUB_POOL_NAME   ?= sub-pool
SUB_POOL_NODES  ?= 1
SUB_POOL_MACHINE ?= e2-standard-2

# infra-init directory (relative to repo root)
INFRA_DIR     ?= ../../infra-init

# Backup / volume-snapshot
REGION        ?= us-east1
NAMESPACE     ?= default
GSA_NAME      ?= cnpg-backup-sa
KSA_NAME      ?= pg-cluster
BUCKET_NAME   ?= cnpg-wal-archive-$(PROJECT_ID)

.PHONY: create delete info \
        install-cnpg-operator install-keda install-metallb install-prometheus install-all \
        add-sub-pool apply-infra grafana-forward setup-gke \
        enable-csi-driver install-snapshot-crds install-snapshot-controller \
        create-gcs-bucket create-backup-gsa create-backup-ksa enable-workload-identity \
        install-barman-plugin apply-backup-resources init-volume-snapshot \
        create-gke-cluster apply-infra-gke init-db init-setup-gke

create:
	@echo "👉 make create (TARGET=$(TARGET), CLUSTER_NAME=$(CLUSTER_NAME))"
ifeq ($(TARGET),local)
	@CLUSTER_NAME=$(CLUSTER_NAME) NODES=$(NODES) \
		./create-local-cluster.sh
else ifeq ($(TARGET),gke)
	@CLUSTER_NAME=$(CLUSTER_NAME) \
	PROJECT_ID=$(PROJECT_ID) \
	ZONE=$(ZONE) \
	NODES=$(NODES) \
	MACHINE_TYPE=$(MACHINE_TYPE) \
	./create-gke-cluster.sh
else
	@echo "❌ Unknown TARGET=$(TARGET) (use local or gke)"
	@exit 1
endif

delete:
	@echo "🗑️  make delete (TARGET=$(TARGET), CLUSTER_NAME=$(CLUSTER_NAME))"
ifeq ($(TARGET),local)
	@kind delete cluster --name $(CLUSTER_NAME)
else ifeq ($(TARGET),gke)
	@gcloud container clusters delete $(CLUSTER_NAME) \
		--zone $(ZONE) \
		--project $(PROJECT_ID) \
		--quiet
endif

info:
	@echo "TARGET=$(TARGET)"
	@echo "CLUSTER_NAME=$(CLUSTER_NAME)"
	@echo "NODES=$(NODES)"

install-cnpg-operator:
	@echo "📦 Installing CNPG Operator..."
	@kubectl apply --server-side -f \
	  https://raw.githubusercontent.com/cloudnative-pg/cloudnative-pg/release-1.28/releases/cnpg-1.28.0.yaml

install-keda:
	@echo "📦 Installing KEDA..."
	@kubectl create -f https://github.com/kedacore/keda/releases/download/v2.19.0/keda-2.19.0.yaml

install-metallb:
	@echo "📦 Installing MetalLB (native)..."
	@kubectl apply -f https://raw.githubusercontent.com/metallb/metallb/v0.13.7/config/manifests/metallb-native.yaml

install-prometheus:
	@echo "📦 Installing Prometheus Stack..."
	@helm repo add prometheus-community https://prometheus-community.github.io/helm-charts 2>/dev/null || true
	@helm repo update
	@helm install prometheus prometheus-community/kube-prometheus-stack \
	  --namespace monitoring --create-namespace \
	  --set alertmanager.enabled=false \
	  --set prometheusOperator.admissionWebhooks.enabled=false \
	  --set prometheusOperator.admissionWebhooks.patch.enabled=false \
	  --set prometheusOperator.tls.enabled=false \
	  --set grafana.enabled=true \
	  --set grafana.adminUser=admin \
	  --set grafana.adminPassword=admin \
	  --set prometheus.prometheusSpec.retention=1d \
	  --set prometheus.prometheusSpec.serviceMonitorSelectorNilUsesHelmValues=false \
	  --set prometheus.prometheusSpec.podMonitorSelectorNilUsesHelmValues=false

add-sub-pool:
	@echo "📦 Adding sub-pool to GKE cluster $(CLUSTER_NAME)..."
	@gcloud container node-pools create $(SUB_POOL_NAME) \
	  --cluster=$(CLUSTER_NAME) \
	  --zone=$(ZONE) \
	  --num-nodes=$(SUB_POOL_NODES) \
	  --machine-type=$(SUB_POOL_MACHINE) \
	  --enable-private-nodes \
	  --disk-size=20 \
	  --disk-type=pd-standard \
	  --workload-metadata=GKE_METADATA

apply-infra:
	@echo "📦 Applying infra-init resources..."
	@kubectl apply -f $(INFRA_DIR)/db-secret.yaml -f $(INFRA_DIR)/cnpg-queries.yaml
	@echo "⏳ Waiting for CNPG operator to be ready..."
	@kubectl rollout status deployment/cnpg-controller-manager -n cnpg-system --timeout=120s
	@kubectl apply -f $(INFRA_DIR)/cnpg-cluster.yaml
	@kubectl apply -f $(INFRA_DIR)/pod-monitor.yaml
	@echo "✅ infra-init applied"

grafana-forward:
	@echo "🌐 Grafana → http://localhost:3000  (admin/admin)"
	@echo "   Dashboard CNPG (ID 20417) đã được tự động provision."
	@kubectl port-forward -n monitoring svc/prometheus-grafana 3000:80

setup-gke: create add-sub-pool install-cnpg-operator install-prometheus apply-infra
	@echo ""
	@echo "✅ GKE lab ready!"
	@echo "   Run: make grafana-forward   — để mở Grafana"

add-gke-nodepool:
	@echo "📦 Adding n2-standard-2 node pool to GKE cluster..."
	@gcloud container node-pools create n2-pool \
	  --cluster=$(CLUSTER_NAME) \
	  --zone=$(ZONE) \
	  --num-nodes=1 \
	  --machine-type=n2-standard-2 \
	  --enable-private-nodes \
	  --disk-size=20 \
	  --disk-type=pd-standard \
	  --workload-metadata=GKE_METADATA

install-all: install-cnpg-operator install-keda install-prometheus install-metallb

# ---------------------------------------------------------------------------
# Volume Snapshot & Backup setup (GKE only)
# ---------------------------------------------------------------------------

enable-csi-driver:
	@echo "📦 Enabling GCE Persistent Disk CSI driver..."
	@gcloud container clusters update $(CLUSTER_NAME) \
	  --update-addons=GcePersistentDiskCsiDriver=ENABLED \
	  --zone $(ZONE)

install-snapshot-crds:
	@echo "📦 Installing VolumeSnapshot CRDs..."
	@kubectl apply -f https://raw.githubusercontent.com/kubernetes-csi/external-snapshotter/master/client/config/crd/snapshot.storage.k8s.io_volumesnapshotclasses.yaml
	@kubectl apply -f https://raw.githubusercontent.com/kubernetes-csi/external-snapshotter/master/client/config/crd/snapshot.storage.k8s.io_volumesnapshotcontents.yaml
	@kubectl apply -f https://raw.githubusercontent.com/kubernetes-csi/external-snapshotter/master/client/config/crd/snapshot.storage.k8s.io_volumesnapshots.yaml

install-snapshot-controller:
	@echo "📦 Installing snapshot controller..."
	@kubectl wait --for=condition=Established crd/volumesnapshotclasses.snapshot.storage.k8s.io --timeout=60s
	@kubectl wait --for=condition=Established crd/volumesnapshotcontents.snapshot.storage.k8s.io --timeout=60s
	@kubectl wait --for=condition=Established crd/volumesnapshots.snapshot.storage.k8s.io --timeout=60s
	@kubectl apply -f https://raw.githubusercontent.com/kubernetes-csi/external-snapshotter/master/deploy/kubernetes/snapshot-controller/rbac-snapshot-controller.yaml
	@kubectl apply -f https://raw.githubusercontent.com/kubernetes-csi/external-snapshotter/master/deploy/kubernetes/snapshot-controller/setup-snapshot-controller.yaml

create-gcs-bucket:
	@echo "🪣 Creating GCS bucket gs://$(BUCKET_NAME)..."
	@if gcloud storage buckets describe gs://$(BUCKET_NAME) > /dev/null 2>&1; then \
	  echo "  → Bucket gs://$(BUCKET_NAME) already exists, skipping."; \
	else \
	  gcloud storage buckets create gs://$(BUCKET_NAME) \
	    --location=$(REGION) \
	    --uniform-bucket-level-access; \
	  echo "  → Bucket gs://$(BUCKET_NAME) created."; \
	fi

create-backup-gsa:
	@echo "🔑 Creating Google Service Account $(GSA_NAME)..."
	@if gcloud iam service-accounts describe $(GSA_NAME)@$(PROJECT_ID).iam.gserviceaccount.com > /dev/null 2>&1; then \
	  echo "  → GSA $(GSA_NAME) already exists, skipping creation."; \
	else \
	  gcloud iam service-accounts create $(GSA_NAME) \
	    --display-name="CNPG Backup Service Account"; \
	fi
	@gcloud storage buckets add-iam-policy-binding gs://$(BUCKET_NAME) \
	  --member="serviceAccount:$(GSA_NAME)@$(PROJECT_ID).iam.gserviceaccount.com" \
	  --role="roles/storage.objectAdmin"
	@gcloud storage buckets add-iam-policy-binding gs://$(BUCKET_NAME) \
	  --member="serviceAccount:$(GSA_NAME)@$(PROJECT_ID).iam.gserviceaccount.com" \
	  --role="roles/storage.admin"

create-backup-ksa:
	@echo "🔑 Creating Kubernetes Service Account $(KSA_NAME)..."
	@if kubectl get serviceaccount $(KSA_NAME) -n $(NAMESPACE) > /dev/null 2>&1; then \
	  echo "  → KSA $(KSA_NAME) already exists, skipping."; \
	else \
	  kubectl create serviceaccount $(KSA_NAME) -n $(NAMESPACE); \
	fi

enable-workload-identity:
	@echo "🔗 Enabling Workload Identity and binding GSA ↔ KSA..."
	@EXISTING_WI=$$(gcloud container clusters describe $(CLUSTER_NAME) \
	  --zone=$(ZONE) \
	  --format="value(workloadIdentityConfig.workloadPool)" 2>/dev/null); \
	if [ -z "$$EXISTING_WI" ]; then \
	  echo "  → Enabling Workload Identity on cluster..."; \
	  gcloud container clusters update $(CLUSTER_NAME) \
	    --zone=$(ZONE) \
	    --workload-pool=$(PROJECT_ID).svc.id.goog; \
	else \
	  echo "  → Workload Identity already enabled ($$EXISTING_WI), skipping."; \
	fi
	@gcloud iam service-accounts add-iam-policy-binding \
	  $(GSA_NAME)@$(PROJECT_ID).iam.gserviceaccount.com \
	  --role="roles/iam.workloadIdentityUser" \
	  --member="serviceAccount:$(PROJECT_ID).svc.id.goog[$(NAMESPACE)/$(KSA_NAME)]"
	@kubectl annotate serviceaccount $(KSA_NAME) -n $(NAMESPACE) \
	  iam.gke.io/gcp-service-account=$(GSA_NAME)@$(PROJECT_ID).iam.gserviceaccount.com

install-barman-plugin:
	@echo "📦 Installing cert-manager and barman cloud plugin..."
	@kubectl apply -f https://github.com/cert-manager/cert-manager/releases/latest/download/cert-manager.yaml
	@kubectl rollout status deployment/cert-manager -n cert-manager --timeout=120s
	@kubectl rollout status deployment/cert-manager-webhook -n cert-manager --timeout=120s
	@kubectl apply -f https://github.com/cloudnative-pg/plugin-barman-cloud/releases/download/v0.11.0/manifest.yaml
	@kubectl wait --for=condition=Established crd/objectstores.barmancloud.cnpg.io --timeout=120s

apply-backup-resources:
	@echo "📦 Applying ObjectStore and ScheduledBackup..."
	@kubectl apply -f $(INFRA_DIR)/objectstore.yaml
	@kubectl apply -f $(INFRA_DIR)/backup.yaml
	@echo "✅ Backup resources applied"

init-volume-snapshot: enable-csi-driver install-snapshot-crds install-snapshot-controller \
                      create-gcs-bucket create-backup-gsa create-backup-ksa \
                      enable-workload-identity install-barman-plugin apply-backup-resources
	@echo ""
	@echo "✅ Volume snapshot & backup setup complete!"

# ---------------------------------------------------------------------------
# Full GKE lab init (Steps 1-7)
# ---------------------------------------------------------------------------

# Step 1: Create GKE cluster (1 node default-pool for loadgen) + pg-pool (5 nodes for DB)
create-gke-cluster:
	@echo "☁️  Step 1: Creating GKE cluster $(CLUSTER_NAME)..."
	@gcloud container clusters create $(CLUSTER_NAME) \
	  --zone=$(ZONE) \
	  --no-enable-autoupgrade \
	  --machine-type=$(MACHINE_TYPE) \
	  --num-nodes=1 \
	  --disk-type=pd-standard --disk-size=20 \
	  --enable-ip-alias \
	  --workload-pool="$(PROJECT_ID).svc.id.goog" \
	  --workload-metadata=GKE_METADATA
	@echo "  → Creating pg-pool (5 nodes for DB instances)..."
	@gcloud container node-pools create pg-pool \
	  --cluster=$(CLUSTER_NAME) \
	  --zone=$(ZONE) \
	  --node-locations=us-east1-c \
	  --machine-type=$(MACHINE_TYPE) \
	  --num-nodes=5 \
	  --disk-type=pd-standard --disk-size=20 \
	  --enable-private-nodes \
	  --workload-metadata=GKE_METADATA
	@echo "✅ Cluster and pg-pool created"

# Step 5: Patch PROJECT_ID in manifests, detect loadgen node, apply infra resources
apply-infra-gke:
	@echo "📦 Step 5: Patching manifests and applying infra resources..."
	@OLD_ID=$$(grep -oP 'project-[a-z0-9-]+' $(INFRA_DIR)/objectstore.yaml | head -1); \
	NEW_ID=$(PROJECT_ID); \
	if [ -n "$$OLD_ID" ] && [ "$$OLD_ID" != "$$NEW_ID" ]; then \
	  echo "  → Replacing $$OLD_ID → $$NEW_ID in objectstore.yaml and cnpg-cluster.yaml"; \
	  sed -i "s/$$OLD_ID/$$NEW_ID/g" $(INFRA_DIR)/objectstore.yaml $(INFRA_DIR)/cnpg-cluster.yaml; \
	fi
	@LOADGEN_NODE=$$(kubectl get nodes -o jsonpath='{.items[*].metadata.name}' | tr ' ' '\n' | grep 'default-pool' | head -1); \
	echo "  → Loadgen node: $$LOADGEN_NODE"; \
	sed -i "s/- gke-auto-scaling-lab-default-pool-[a-z0-9-]*/- $$LOADGEN_NODE/" $(INFRA_DIR)/cnpg-cluster.yaml
	@kubectl apply -f $(INFRA_DIR)/volumesnapshotclass.yaml -f $(INFRA_DIR)/objectstore.yaml
	@kubectl apply -f $(INFRA_DIR)/db-secret.yaml -f $(INFRA_DIR)/cnpg-queries.yaml -f $(INFRA_DIR)/pod-monitor.yaml
	@echo "⏳ Waiting for CNPG operator to be ready..."
	@kubectl rollout status deployment/cnpg-controller-manager -n cnpg-system --timeout=120s
	@kubectl apply -f $(INFRA_DIR)/cnpg-cluster.yaml
	@echo "⏳ Waiting for CNPG cluster to be ready..."
	@kubectl wait cluster/pg-cluster --for=condition=Ready --timeout=300s
	@echo "✅ Infra resources applied"

# Step 6: Initialize pgbench data (scale factor 50)
init-db:
	@echo "🐘 Step 6: Initializing pgbench data (scale factor 50)..."
	@kubectl run pgbench-init \
	  --rm \
	  --restart=Never \
	  --image=postgres:16 \
	  --env="PGPASSWORD=password" \
	  -it -- pgbench -i -s 50 postgres://app:password@pg-cluster-rw/app
	@echo "✅ pgbench data initialized"

# Full init: Steps 1-7
init-setup-gke: create-gke-cluster \
                install-cnpg-operator \
                install-prometheus \
                enable-csi-driver install-snapshot-crds install-snapshot-controller install-barman-plugin \
                create-gcs-bucket create-backup-gsa create-backup-ksa enable-workload-identity \
                apply-infra-gke \
                init-db \
                apply-backup-resources
	@echo ""
	@echo "✅ GKE lab fully initialized!"
	@echo "   Run: make grafana-forward   — để mở Grafana"
