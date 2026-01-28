TARGET        ?= local   # local | gke
CLUSTER_NAME  ?= local-pg
NODES         ?= 1

# GKE only
PROJECT_ID    ?= my-project
ZONE          ?= asia-southeast1-a
MACHINE_TYPE  ?= e2-standard-2

.PHONY: create delete info

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
