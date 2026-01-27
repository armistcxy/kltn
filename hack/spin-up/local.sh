#!/usr/bin/env bash
set -euo pipefail

CLUSTER_NAME="${CLUSTER_NAME:-local-pg}"
NODES="${NODES:-1}"

if [ "$NODES" -lt 1 ]; then
  echo "❌ NODES must be >= 1"
  exit 1
fi

echo "Creating kind cluster '${CLUSTER_NAME}' with ${NODES} node(s)"

# Generate kind config
{
  echo "kind: Cluster"
  echo "apiVersion: kind.x-k8s.io/v1alpha4"
  echo "nodes:"
  echo "  - role: control-plane"

  # worker nodes
  for ((i=1; i<"$NODES"; i++)); do
    echo "  - role: worker"
  done
} | kind create cluster \
      --name "${CLUSTER_NAME}" \
      --wait 60s \
      --config -

echo "✅ Cluster created successfully"

kubectl get nodes
