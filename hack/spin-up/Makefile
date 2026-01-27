CLUSTER_NAME ?= local-pg
NODES        ?= 1

.PHONY: create delete

create:
	@echo "👉 make create (CLUSTER_NAME=$(CLUSTER_NAME), NODES=$(NODES))"
	@CLUSTER_NAME=$(CLUSTER_NAME) NODES=$(NODES) ./create-local-cluster.sh

delete:
	@echo "🗑️  make delete (CLUSTER_NAME=$(CLUSTER_NAME))"
	@kind delete cluster --name $(CLUSTER_NAME)
