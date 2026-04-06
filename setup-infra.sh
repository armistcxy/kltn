#!/bin/bash
set -e

# Init cluster with 3 nodes
cd hack/spin-up
NODES=3 CLUSTER_NAME=test make create
make install-all

sleep 30

# Create CNPG cluster, local IP for cnpg service and PodMonitor for watching metrics expose from db cluster
cd ../../keda-approach
kubectl apply -f metallb-config.yaml -f db-secret.yaml -f cnpg-cluster.yaml -f pod-monitor.yaml

# Patch service to LoadBalancer
echo "Patching services to LoadBalancer..."
kubectl patch svc pg-cluster-r -p '{"spec": {"type": "LoadBalancer"}}'
kubectl patch svc pg-cluster-rw -p '{"spec": {"type": "LoadBalancer"}}'

# Wait to get External IP
get_ip() {
    local svc=$1
    echo "Waiting for $svc External IP..."
    while true; do
        IP=$(kubectl get svc $svc -o jsonpath='{.status.loadBalancer.ingress[0].ip}')
        [ -n "$IP" ] && echo "$IP" && break
        sleep 5
    done
}

export R_ADDR=$(get_ip pg-cluster-r)
export RW_ADDR=$(get_ip pg-cluster-rw)

echo "RW_ADDR=$RW_ADDR"
echo "R_ADDR=$R_ADDR"

# Init data for DB
pgedge-loadgen init \
    --app wholesale \
    --size 5GB \
    --connection "postgres://app:password@$RW_ADDR:5432/app"
