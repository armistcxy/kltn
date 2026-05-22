# ── Stage 1: Build ────────────────────────────────────────────────────────────
FROM golang:1.25-alpine AS builder

WORKDIR /build

# Install git for go mod download (some deps fetch via VCS).
RUN apk add --no-cache git

# Cache module downloads separately from source.
COPY go.mod go.sum ./
RUN go mod download

# Copy entire repo (auto-run is part of the main module).
COPY . .

# Build the auto-run server binary.
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -trimpath -ldflags="-s -w" \
    -o /auto-run-server \
    ./auto-run/cmd/server

# ── Stage 2: Runtime ──────────────────────────────────────────────────────────
FROM alpine:3.21

# kubectl is needed to port-forward Prometheus (optional, remove if not needed).
# git is needed for the init container pattern if used directly.
RUN apk add --no-cache ca-certificates curl git && \
    curl -LO "https://dl.k8s.io/release/$(curl -L -s https://dl.k8s.io/release/stable.txt)/bin/linux/amd64/kubectl" && \
    chmod +x kubectl && mv kubectl /usr/local/bin/

COPY --from=builder /auto-run-server /usr/local/bin/auto-run-server
COPY auto-run/ui /ui
COPY loadgen/scenarios /repo/loadgen/scenarios
COPY scale-controller-config /repo/scale-controller-config

# Default env - override in Deployment.
ENV MATRIX_FILE=/config/matrix.yaml
ENV REPO_ROOT=/repo
ENV RESULTS_DIR=/results
ENV UI_DIR=/ui

EXPOSE 8080

ENTRYPOINT ["auto-run-server"]
