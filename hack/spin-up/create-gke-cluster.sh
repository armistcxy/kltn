#!/usr/bin/env bash
set -euo pipefail

# ===== Config (env overridable) =====
CLUSTER_NAME="${CLUSTER_NAME:-pg-gke}"
ZONE="${ZONE:-asia-southeast1-a}"
NODES="${NODES:-3}"
MACHINE_TYPE="${MACHINE_TYPE:-e2-standard-4}"
DISK_TYPE="${DISK_TYPE:-pd-ssd}"
DISK_SIZE="${DISK_SIZE:-50}"
PROJECT_ID="${PROJECT_ID:-$(gcloud config get-value project)}"

# ===== Validation =====
if [ -z "$PROJECT_ID" ]; then
  echo "❌ GCP project not set"
  exit 1
fi

if [ "$NODES" -lt 1 ]; then
  echo "❌ NODES must be >= 1"
  exit 1
fi

# ===== Info =====
echo "👉 Creating GKE *zonal* cluster"
echo "   Cluster : $CLUSTER_NAME"
echo "   Project : $PROJECT_ID"
echo "   Zone    : $ZONE"
echo "   Nodes   : $NODES"

# ===== Create cluster =====
gcloud container clusters create "$CLUSTER_NAME" \
  --project "$PROJECT_ID" \
  --zone "$ZONE" \
  --num-nodes "$NODES" \
  --machine-type "$MACHINE_TYPE" \
  --disk-type "$DISK_TYPE" \
  --disk-size "$DISK_SIZE" \
  --enable-ip-alias \
  --workload-pool="${PROJECT_ID}.svc.id.goog" \
  --workload-metadata=GKE_METADATA

# ===== Fetch kubeconfig =====
gcloud container clusters get-credentials "$CLUSTER_NAME" \
  --zone "$ZONE" \
  --project "$PROJECT_ID"

echo "✅ Cluster created successfully"
kubectl get nodes
