TARGET        ?= local# local | gke
CLUSTER_NAME  ?= local-pg
NODES         ?= 1

# GKE only
PROJECT_ID    ?= my-project
ZONE          ?= asia-southeast1-a
MACHINE_TYPE  ?= e2-standard-2

.PHONY: create delete info install-cnpg-operator install-keda install-metallb install-prometheus install-all

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
	@helm repo add prometheus-community https://prometheus-community.github.io/helm-charts
	@helm repo update
	@helm install prometheus prometheus-community/kube-prometheus-stack \
	  --namespace monitoring --create-namespace \
	  --set grafana.adminUser=admin \
	  --set grafana.adminPassword=admin \
	  --set prometheusOperator.resources.requests.cpu=50m \
	  --set prometheusOperator.resources.requests.memory=64Mi \
	  --set prometheusOperator.resources.limits.cpu=200m \
	  --set prometheusOperator.resources.limits.memory=128Mi \
	  --set prometheus.prometheusSpec.replicas=1 \
	  --set prometheus.prometheusSpec.retention=12h \
	  --set prometheus.prometheusSpec.resources.requests.cpu=100m \
	  --set prometheus.prometheusSpec.resources.requests.memory=256Mi \
	  --set prometheus.prometheusSpec.resources.limits.cpu=500m \
	  --set prometheus.prometheusSpec.resources.limits.memory=512Mi \
	  --set alertmanager.enabled=false \
	  --set grafana.resources.requests.cpu=50m \
	  --set grafana.resources.requests.memory=64Mi \
	  --set grafana.resources.limits.cpu=200m \
	  --set grafana.resources.limits.memory=128Mi \
	  --set kube-state-metrics.resources.requests.cpu=10m \
	  --set kube-state-metrics.resources.requests.memory=32Mi \
	  --set kube-state-metrics.resources.limits.cpu=100m \
	  --set kube-state-metrics.resources.limits.memory=64Mi \
	  --set prometheus-node-exporter.resources.requests.cpu=10m \
	  --set prometheus-node-exporter.resources.requests.memory=32Mi \
	  --set prometheus-node-exporter.resources.limits.cpu=100m \
	  --set prometheus-node-exporter.resources.limits.memory=64Mi

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
