#!/usr/bin/env bash
set -euo pipefail

# Installs hyve's controller + API onto whatever cluster your current
# kubectl context points at — intended for the k3d cluster
# scripts/create-local-cluster.sh creates (run that first), or any other
# dev cluster sharing your local Docker image store, so no registry push is
# needed. Builds deploy/Dockerfile.dev (from THIS repo's local source, not
# a published release — see that file's header comment), applies CRDs, and
# helm upgrade --installs both deploy/helm/hyve-controller and
# deploy/helm/hyve-api.
#
# Exposes hyve-api via Ingress (host: hyve-api.127.0.0.1.nip.io by
# default), not NodePort — Docker Desktop's own built-in Kubernetes doesn't
# map any host port beyond its fixed API-server one (confirmed live:
# NodePort and LoadBalancer were both unreachable from the host on it), so
# this only actually works against create-local-cluster.sh's k3d cluster,
# whose host 80/443 are mapped to k3d's own load balancer -> Traefik (k3d's
# default ingress controller) at cluster-creation time — the only point
# port mappings can be set at all for a kind/k3d node. Every service
# exposed *after* that one-time setup, including this one, is pure
# `kubectl apply`/`helm upgrade --install` — no further cluster changes,
# ever.
#
# nip.io (a public wildcard-DNS service resolving any
# <name>.127.0.0.1.nip.io to 127.0.0.1 via real DNS), not a bare
# *.localhost name — confirmed live that curl has its own hardcoded
# special-case for .localhost that doesn't reflect real resolution: macOS's
# actual system resolver (dscacheutil) and Go's net.LookupHost (i.e. the
# real hyve binary, cgo resolver or not) both fail to resolve
# hyve-api.localhost, only nip.io worked consistently across every tool.
# Needs a working internet connection for the DNS lookup itself (not for
# the actual traffic, which still goes straight to 127.0.0.1); use
# HYVE_INSTALL_INGRESS_HOST to override if you'd rather manage your own
# /etc/hosts entry instead.
#
# Unlike scripts/test-*.sh, this does NOT clean up after itself — it's
# meant to leave a running install behind. Re-running it is safe/idempotent
# (helm upgrade --install, kubectl apply, and the credentials Secret is
# only created if missing so re-running never rotates it out from under
# you).
#
# Usage: scripts/install-local.sh [namespace]
#   namespace   defaults to hyve-system

NAMESPACE="${1:-hyve-system}"
IMAGE_TAG="hyve:dev"
PRIMARY_CLUSTER_NAME="${HYVE_INSTALL_PRIMARY_CLUSTER_NAME:-local}"
INGRESS_HOST="${HYVE_INSTALL_INGRESS_HOST:-hyve-api.127.0.0.1.nip.io}"
PUBLIC_BASE_URL="${HYVE_INSTALL_PUBLIC_BASE_URL:-http://$INGRESS_HOST}"

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

log() { echo "── $*"; }

# The image build/run platform here is independent of what the *cluster*
# runs — Kubernetes pulls images via containerd, not this shell's `docker`
# CLI, so a broken DOCKER_DEFAULT_PLATFORM (if you have one — check with
# `env | grep DOCKER_DEFAULT_PLATFORM`) only affects this build step, never
# the Deployment itself. Passed explicitly so this script doesn't depend on
# that env var being sane.
DOCKER_PLATFORM="linux/$(docker version --format '{{.Server.Arch}}')"

log "Building $IMAGE_TAG from local source (platform $DOCKER_PLATFORM)"
docker build --platform "$DOCKER_PLATFORM" --load -f "$ROOT_DIR/deploy/Dockerfile.dev" -t "$IMAGE_TAG" "$ROOT_DIR"

# k3d nodes run their own containerd, entirely separate from the host
# Docker daemon's image store — unlike Docker Desktop's own built-in
# Kubernetes, `docker build --load` alone does NOT make the image visible
# inside a k3d cluster. Without this, kubelet tries to pull "hyve:dev" from
# Docker Hub (where it obviously doesn't exist) and every pod ErrImagePulls
# — confirmed live. `k3d image import` copies the image directly into the
# cluster's containerd, no registry involved. Skipped entirely when the
# current context isn't a k3d cluster (e.g. Docker Desktop's built-in one,
# which doesn't need or support this).
CURRENT_CONTEXT="$(kubectl config current-context)"
if [[ "$CURRENT_CONTEXT" == k3d-* ]]; then
  K3D_CLUSTER="${CURRENT_CONTEXT#k3d-}"
  log "Importing $IMAGE_TAG into k3d cluster '$K3D_CLUSTER'"
  k3d image import "$IMAGE_TAG" -c "$K3D_CLUSTER"
fi

log "Ensuring namespace $NAMESPACE exists"
kubectl create namespace "$NAMESPACE" --dry-run=client -o yaml | kubectl apply -f - >/dev/null

log "Applying CRDs"
kubectl apply -f "$ROOT_DIR/deploy/crds/" >/dev/null

log "Ensuring hyve-api-credentials Secret exists (never rotated on re-run)"
if ! kubectl -n "$NAMESPACE" get secret hyve-api-credentials >/dev/null 2>&1; then
  kubectl -n "$NAMESPACE" create secret generic hyve-api-credentials \
    --from-literal=session-signing-key="$(openssl rand -hex 32)" >/dev/null
  log "  created a fresh session-signing-key"
else
  log "  already exists, left untouched"
fi

# :dev is a floating tag this script overwrites on every run, so
# IfNotPresent + a rollout restart alone wouldn't guarantee the freshest
# content gets used. The right fix differs by cluster type though:
#   - Docker Desktop's built-in Kubernetes shares the same containerd image
#     store `docker build --load` just wrote to directly — Always safely
#     resolves locally with no real network round trip.
#   - k3d nodes run their own separate containerd — `k3d image import`
#     above is what refreshes it, and it fully replaces the tag's local
#     content each time, so IfNotPresent (skip anything that would touch a
#     real registry) is correct there. Always would be actively wrong: k3s
#     would try to verify against docker.io, find no such repository, and
#     ErrImagePullBackOff even though a perfectly good image already sits
#     in its own local containerd — confirmed live.
if [[ "$CURRENT_CONTEXT" == k3d-* ]]; then
  PULL_POLICY="IfNotPresent"
else
  PULL_POLICY="Always"
fi

log "Installing hyve-controller (image.pullPolicy=$PULL_POLICY)"
helm upgrade --install hyve-controller "$ROOT_DIR/deploy/helm/hyve-controller" \
  --namespace "$NAMESPACE" \
  --set image.repository=hyve --set image.tag=dev --set image.pullPolicy="$PULL_POLICY" \
  --set namespace="$NAMESPACE" >/dev/null

log "Installing hyve-api (primary-cluster-name=$PRIMARY_CLUSTER_NAME, public-base-url=$PUBLIC_BASE_URL)"
helm upgrade --install hyve-api "$ROOT_DIR/deploy/helm/hyve-api" \
  --namespace "$NAMESPACE" \
  --set image.repository=hyve --set image.tag=dev --set image.pullPolicy="$PULL_POLICY" \
  --set namespace="$NAMESPACE" \
  --set primaryClusterName="$PRIMARY_CLUSTER_NAME" \
  --set publicBaseURL="$PUBLIC_BASE_URL" \
  --set ingress.enabled=true \
  --set ingress.host="$INGRESS_HOST" >/dev/null

log "Restarting both Deployments so the freshly-built image is actually used"
kubectl -n "$NAMESPACE" rollout restart deployment/hyve-controller deployment/hyve-api

log "Waiting for rollout"
kubectl -n "$NAMESPACE" rollout status deployment/hyve-controller --timeout=90s
kubectl -n "$NAMESPACE" rollout status deployment/hyve-api --timeout=90s

echo ""
echo "✅ Installed. API reachable directly at $PUBLIC_BASE_URL (via Ingress — no port-forward needed)."
echo ""
echo "Next steps:"
echo ""
echo "  1. Create a user:"
echo "     (cd $ROOT_DIR && go run . cluster-config api create-user <username> --role admin --namespace $NAMESPACE) | kubectl apply -f -"
echo ""
echo "  2. Log in:"
echo "     hyve login --api-url $PUBLIC_BASE_URL"
echo ""
echo "  3. Try the primary-cluster kubeconfig path (finally live-verifiable — see"
echo "     HYVE-IMPLEMENTATION-PLAN.md's Phase 6.5 status note):"
echo "     curl \"$PUBLIC_BASE_URL/api/kubeconfig?cluster=$PRIMARY_CLUSTER_NAME\" \\"
echo "       -H \"Authorization: Bearer \$(jq -r .token ~/.hyve/session.json)\" > /tmp/hyve-primary.kubeconfig"
echo "     kubectl --kubeconfig /tmp/hyve-primary.kubeconfig get pods -n $NAMESPACE"
