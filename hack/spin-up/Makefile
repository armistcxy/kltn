TARGET        ?= local# local | gke
CLUSTER_NAME  ?= auto-scaling-lab
NODES         ?= 1

# GKE only
PROJECT_ID    ?= my-project
ZONE          ?= us-east1
MACHINE_TYPE  ?= e2-standard-2

# Sub-pool (quota workaround)
SUB_POOL_NAME ?= sub-pool
SUB_POOL_NODES ?= 1
SUB_POOL_MACHINE ?= e2-standard-2

# infra-init directory (relative to repo root)
INFRA_DIR     ?= ../../infra-init

# Backup / volume-snapshot
REGION        ?= us-east1
NAMESPACE     ?= default
GSA_NAME      ?= cnpg-backup-sa
KSA_NAME      ?= cnpg-backup-ksa
BUCKET_NAME   ?= cnpg-wal-archive-$(PROJECT_ID)

.PHONY: create delete info \
        install-cnpg-operator install-keda install-metallb install-prometheus install-all \
        add-sub-pool apply-infra grafana-forward setup-gke \
        enable-csi-driver install-snapshot-crds install-snapshot-controller \
        create-gcs-bucket create-backup-gsa create-backup-ksa enable-workload-identity \
        install-barman-plugin apply-backup-resources init-volume-snapshot

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
	  -f prometheus-values.yaml

add-sub-pool:
	@echo "📦 Adding sub-pool to GKE cluster $(CLUSTER_NAME)..."
	@gcloud container node-pools create $(SUB_POOL_NAME) \
	  --cluster=$(CLUSTER_NAME) \
	  --zone=$(ZONE) \
	  --num-nodes=$(SUB_POOL_NODES) \
	  --machine-type=$(SUB_POOL_MACHINE) \
	  --enable-private-nodes \
	  --disk-size=20 \
	  --disk-type=pd-standard

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
	  --disk-type=pd-standard

install-all: install-cnpg-operator install-keda install-prometheus install-metallb

# ---------------------------------------------------------------------------
# Volume Snapshot & Backup setup (GKE only)
# ---------------------------------------------------------------------------

enable-csi-driver:
	@echo "📦 Enabling GCE Persistent Disk CSI driver..."
	@gcloud container clusters update $(CLUSTER_NAME) \
	  --update-addons=GcePersistentDiskCsiDriver=ENABLED \
	  --region $(REGION)

install-snapshot-crds:
	@echo "📦 Installing VolumeSnapshot CRDs..."
	@kubectl apply -f https://raw.githubusercontent.com/kubernetes-csi/external-snapshotter/master/client/config/crd/snapshot.storage.k8s.io_volumesnapshotclasses.yaml
	@kubectl apply -f https://raw.githubusercontent.com/kubernetes-csi/external-snapshotter/master/client/config/crd/snapshot.storage.k8s.io_volumesnapshotcontents.yaml
	@kubectl apply -f https://raw.githubusercontent.com/kubernetes-csi/external-snapshotter/master/client/config/crd/snapshot.storage.k8s.io_volumesnapshots.yaml

install-snapshot-controller:
	@echo "📦 Installing snapshot controller..."
	@kubectl apply -f https://raw.githubusercontent.com/kubernetes-csi/external-snapshotter/master/deploy/kubernetes/snapshot-controller/rbac-snapshot-controller.yaml
	@kubectl apply -f https://raw.githubusercontent.com/kubernetes-csi/external-snapshotter/master/deploy/kubernetes/snapshot-controller/setup-snapshot-controller.yaml
	@kubectl apply -f $(INFRA_DIR)/volumesnapshotclass.yaml

create-gcs-bucket:
	@echo "🪣 Creating GCS bucket gs://$(BUCKET_NAME)..."
	@gcloud storage buckets create gs://$(BUCKET_NAME) \
	  --location=$(REGION) \
	  --uniform-bucket-level-access
	@gcloud storage buckets describe gs://$(BUCKET_NAME)

create-backup-gsa:
	@echo "🔑 Creating Google Service Account $(GSA_NAME)..."
	@gcloud iam service-accounts create $(GSA_NAME) \
	  --display-name="CNPG Backup Service Account"
	@gcloud storage buckets add-iam-policy-binding gs://$(BUCKET_NAME) \
	  --member="serviceAccount:$(GSA_NAME)@$(PROJECT_ID).iam.gserviceaccount.com" \
	  --role="roles/storage.objectAdmin"

create-backup-ksa:
	@echo "🔑 Creating Kubernetes Service Account $(KSA_NAME)..."
	@kubectl create serviceaccount $(KSA_NAME) -n $(NAMESPACE)

enable-workload-identity:
	@echo "🔗 Enabling Workload Identity and binding GSA ↔ KSA..."
	@gcloud container clusters update $(CLUSTER_NAME) \
	  --region=$(REGION) \
	  --workload-pool=$(PROJECT_ID).svc.id.goog
	@gcloud container node-pools update default-pool \
	  --cluster=$(CLUSTER_NAME) \
	  --region=$(REGION) \
	  --workload-metadata=GKE_METADATA
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
	@kubectl apply -f https://github.com/cloudnative-pg/plugin-barman-cloud/releases/download/v0.11.0/manifest.yaml

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
