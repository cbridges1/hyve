#!/usr/bin/env bash
set -euo pipefail

# Creates (idempotently) a standalone k3d cluster named hyve-local for
# local hyve development — separate from Docker Desktop's own built-in
# Kubernetes, which doesn't expose any way to map host ports beyond its
# fixed API-server port (confirmed live: its node container only has 6443
# mapped, and neither NodePort nor LoadBalancer services were reachable
# from the host on it).
#
# Maps host ports 80/443 to k3d's own load balancer container once, at
# creation time — the only point at which k3d (or kind) can be told about
# a port mapping at all; neither supports adding one to a running cluster.
# Everything AFTER this one-time setup is pure `kubectl apply`: k3d ships
# Traefik as its default ingress controller already listening behind that
# load balancer, so exposing a new service later is just an Ingress
# resource routing a hostname to it — see deploy/helm/hyve's optional
# api.ingress.enabled template. No new port to pick, no cluster change, ever
# again.
#
# Sets your kubectl context to this cluster when done. Your Docker Desktop
# Kubernetes context is untouched and still switchable back to via
# `kubectl config use-context docker-desktop`.

CLUSTER_NAME="${1:-hyve-local}"

log() { echo "── $*"; }

if k3d cluster list "$CLUSTER_NAME" >/dev/null 2>&1; then
  log "k3d cluster '$CLUSTER_NAME' already exists — leaving it as-is"
else
  log "Creating k3d cluster '$CLUSTER_NAME' (host 80/443 -> loadbalancer -> Traefik)"
  k3d cluster create "$CLUSTER_NAME" \
    -p "80:80@loadbalancer" \
    -p "443:443@loadbalancer"
fi

kubectl config use-context "k3d-$CLUSTER_NAME" >/dev/null
log "kubectl context set to k3d-$CLUSTER_NAME"

echo ""
echo "✅ Cluster ready. Next: task install:local"
