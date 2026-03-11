BINARY   := loadgen
MODULE   := github.com/armistcxy/loadgen
VERSION  := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS  := -ldflags "-X main.version=$(VERSION)"

.PHONY: build test test-verbose lint vet fmt tidy clean run-example

build:
	go build $(LDFLAGS) -o $(BINARY) ./cmd/loadgen

test:
	go test ./...

test-verbose:
	go test -v -count=1 ./...

test-race:
	go test -race ./...

lint: vet
	@which golangci-lint >/dev/null 2>&1 && golangci-lint run || echo "golangci-lint not installed, running go vet only"

vet:
	go vet ./...

fmt:
	go fmt ./...

tidy:
	go mod tidy

clean:
	rm -f $(BINARY)

# Quick smoke test — requires DB_URL env variable to be set.
run-example: build
	@test -n "$(DB_URL)" || (echo "DB_URL is not set" && exit 1)
	./$(BINARY) run \
		--db-url "$(DB_URL)" \
		--workload pgbench-oltp \
		--concurrency 10 \
		--duration 30s \
		--metrics-port 9090

# Run with a ramp scenario.
run-ramp: build
	@test -n "$(DB_URL)" || (echo "DB_URL is not set" && exit 1)
	./$(BINARY) run \
		--db-url "$(DB_URL)" \
		--workload pgbench-oltp \
		--concurrency 50 \
		--scenario scenarios/ramp.yaml \
		--metrics-port 9090
