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
GKE_PROJECT_ID ?= my-project
GKE_CLUSTER    ?= auto-scaling-lab
GKE_ZONE       ?= us-east1

.PHONY: infra-setup-gke infra-apply infra-delete infra-grafana

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
