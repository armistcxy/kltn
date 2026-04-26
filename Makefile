.PHONY: build test fmt vet tidy

build:
	go build ./cmd/scale-controller

test:
	go test ./...

test-v:
	go test ./... -v

fmt:
	go fmt ./...

vet:
	go vet ./...

tidy:
	go mod tidy

# Default: override on command line: make infra-setup-gke PROJECT_ID=xxx
GKE_PROJECT_ID ?= $(shell gcloud config get-value project 2>/dev/null)
GKE_CLUSTER    ?= auto-scaling-lab
GKE_ZONE       ?= us-east1-b

.PHONY: infra-init infra-init-gke infra-setup-gke infra-apply infra-delete infra-grafana

infra-init: infra-init-gke

infra-init-gke:
	$(MAKE) -C hack/spin-up init-setup-gke \
	  PROJECT_ID=$(GKE_PROJECT_ID) \
	  CLUSTER_NAME=$(GKE_CLUSTER) \
	  ZONE=$(GKE_ZONE)

infra-setup-gke:
	$(MAKE) -C hack/spin-up setup-gke \
	  TARGET=gke \
	  PROJECT_ID=$(GKE_PROJECT_ID) \
	  CLUSTER_NAME=$(GKE_CLUSTER) \
	  ZONE=$(GKE_ZONE)

infra-apply:
	$(MAKE) -C hack/spin-up apply-infra

infra-delete:
	$(MAKE) -C hack/spin-up delete \
	  TARGET=gke \
	  PROJECT_ID=$(GKE_PROJECT_ID) \
	  CLUSTER_NAME=$(GKE_CLUSTER) \
	  ZONE=$(GKE_ZONE)

infra-grafana:
	$(MAKE) -C hack/spin-up grafana-forward

.PHONY: run-pgbench

run-pgbench:
	docker run --rm -it zzzsleepzzz/pgbench-only bash

pgbench-shell:
	kubectl run -it pgbench --image=postgres:16 -- bash
