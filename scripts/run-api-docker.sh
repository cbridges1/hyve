#!/usr/bin/env bash
set -euo pipefail

# Runs hyve-api as a standalone Docker container — no Kubernetes
# Deployment/Pod of its own at all (--home-cluster=none, Milestone 10 Part
# C, HYVE-ORGANIZATION-MODEL-IMPLEMENTATION-PLAN.md's nexus-config/docs) —
# pointed at an existing k3d cluster purely as a registered reconciling
# cluster (Milestone 6). hyve-controller is deliberately NOT started here:
# this task is scoped to the API server alone, matching how it was asked
# for. See scripts/test-docker-k3d.sh's own header comment for the fuller
# "what this does and doesn't prove" reasoning about the controller/API
# asymmetry, if you also want a controller reconciling against the same
# cluster — that script is a self-cleaning integration test, not a
# persistent dev task; this one is the persistent counterpart, mirroring
# scripts/install-local.sh's own "leave a running install behind, safe to
# re-run" shape rather than scripts/test-*.sh's "clean up on exit" one.
#
# Builds deploy/Dockerfile.dev (same file/tag, "hyve:dev", as
# install-local.sh — from THIS repo's own local source, not a published
# release), so re-running this after local code changes always picks them
# up. orgdb lives on a named Docker volume that this script never removes
# — re-running it recreates the container but never loses data, the same
# "safe to re-run" precedent install-local.sh's own Deployment restart
# already establishes.
#
# Usage: scripts/run-api-docker.sh [k3d-cluster-name]
#   k3d-cluster-name   the k3d cluster (its `k3d cluster list` name, no
#                      "k3d-" prefix) to register as the reconciling
#                      cluster — defaults to derived from your current
#                      kubectl context if it's a k3d-* one; required
#                      otherwise. Must already exist — run
#                      `task cluster:local` (or `k3d cluster create
#                      <name>`) first if it doesn't.
#
# Env var overrides:
#   HYVE_API_DOCKER_PORT             host port hyve-api listens on (default 18090)
#   HYVE_API_DOCKER_PUBLIC_BASE_URL  this API's own public address (default http://localhost:$PORT)

CONTAINER_NAME="hyve-api-docker"
ORGDB_VOLUME="hyve-api-docker-orgdb"
IMAGE_TAG="hyve:dev"
PORT="${HYVE_API_DOCKER_PORT:-18090}"
PUBLIC_BASE_URL="${HYVE_API_DOCKER_PUBLIC_BASE_URL:-http://localhost:$PORT}"

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

log() { echo "── $*"; }

# Resolve which k3d cluster to register, same "derive from the current
# kubectl context, or require it explicitly" precedent install-local.sh's
# own k3d-image-import step already uses.
if [[ $# -ge 1 ]]; then
  K3D_CLUSTER="$1"
else
  CURRENT_CONTEXT="$(kubectl config current-context 2>/dev/null || true)"
  if [[ "$CURRENT_CONTEXT" == k3d-* ]]; then
    K3D_CLUSTER="${CURRENT_CONTEXT#k3d-}"
  else
    echo "❌ No k3d cluster name given, and the current kubectl context ($CURRENT_CONTEXT) isn't a k3d-* one." >&2
    echo "   Usage: scripts/run-api-docker.sh [k3d-cluster-name]" >&2
    exit 1
  fi
fi
if ! k3d cluster list "$K3D_CLUSTER" >/dev/null 2>&1; then
  echo "❌ k3d cluster '$K3D_CLUSTER' not found — run 'task cluster:local' or 'k3d cluster create $K3D_CLUSTER' first." >&2
  exit 1
fi

# Passed explicitly to every docker build/run below, not left to Docker's
# own default resolution — some environments carry a broken
# DOCKER_DEFAULT_PLATFORM (confirmed live during this project's own
# Milestone 11 work: one observed value was the literal string
# "linux/amd64export", not a valid platform at all), which silently sends
# `docker run` down a registry-pull path instead of using the image
# `docker build --load` already put in the local store, failing with a
# misleading "pull access denied" error that has nothing to do with the
# real problem.
DOCKER_PLATFORM="linux/$(docker version --format '{{.Server.Arch}}')"

log "Building $IMAGE_TAG from local source (platform $DOCKER_PLATFORM) — same image install-local.sh builds, so both tasks share one Docker layer cache"
docker build --platform "$DOCKER_PLATFORM" --load -f "$ROOT_DIR/deploy/Dockerfile.dev" -t "$IMAGE_TAG" "$ROOT_DIR" >/dev/null

# host.docker.internal + insecure-skip-tls-verify: the same cross-cluster
# reachability trick scripts/test-docker-k3d.sh already established — a
# container on the Docker default bridge network can't resolve the k3d
# cluster's own internal Docker network hostname, and that cluster's own
# serving certificate has no SAN for host.docker.internal. Saved under
# ~/.hyve/kubeconfigs/, the same directory `hyve cluster auth` already
# writes minted kubeconfigs to (see install-local.sh's own next-steps
# text) — a stable, persistent location, not a tempdir this script would
# otherwise need to remember to point you back at.
SERVERLB_PORT=$(docker port "k3d-$K3D_CLUSTER-serverlb" 6443/tcp | head -1 | grep -oE '[0-9]+$')
if [ -z "$SERVERLB_PORT" ]; then
  echo "❌ Could not determine k3d-$K3D_CLUSTER-serverlb's own mapped host port for 6443/tcp" >&2
  exit 1
fi
log "k3d cluster '$K3D_CLUSTER' reachable from containers at host.docker.internal:$SERVERLB_PORT"

KUBECONFIG_DIR="$HOME/.hyve/kubeconfigs"
mkdir -p "$KUBECONFIG_DIR"
RECONCILING_KUBECONFIG="$KUBECONFIG_DIR/$K3D_CLUSTER-reconciling.yaml"
python3 - "$K3D_CLUSTER" "$SERVERLB_PORT" "$RECONCILING_KUBECONFIG" <<'PYEOF'
import subprocess, sys, yaml
cluster, port, dst = sys.argv[1], sys.argv[2], sys.argv[3]
raw = subprocess.run(["k3d", "kubeconfig", "get", cluster], check=True, capture_output=True, text=True).stdout
doc = yaml.safe_load(raw)
for c in doc.get("clusters", []):
    c["cluster"]["server"] = f"https://host.docker.internal:{port}"
    c["cluster"]["insecure-skip-tls-verify"] = True
    c["cluster"].pop("certificate-authority-data", None)
with open(dst, "w") as f:
    yaml.safe_dump(doc, f)
PYEOF
log "Reconciling-cluster kubeconfig written to $RECONCILING_KUBECONFIG"

log "Recreating $CONTAINER_NAME (orgdb volume $ORGDB_VOLUME is never removed — data survives)"
docker rm -f "$CONTAINER_NAME" >/dev/null 2>&1 || true
docker run -d --name "$CONTAINER_NAME" --platform "$DOCKER_PLATFORM" \
  -v "$ORGDB_VOLUME:/data" \
  -p "$PORT:8090" \
  "$IMAGE_TAG" \
  cluster-config api run \
    --namespace=hyve-system \
    --db=sqlite --db-dsn=/data/orgdb.sqlite \
    --home-cluster=none \
    --bind-address=:8090 \
    --public-base-url="$PUBLIC_BASE_URL" >/dev/null

log "Waiting for hyve-api to become healthy"
HEALTHY=""
for _ in $(seq 1 30); do
  if curl -sf "http://localhost:$PORT/healthz" >/dev/null 2>&1; then
    HEALTHY=1
    break
  fi
  sleep 1
done
if [ -z "$HEALTHY" ]; then
  echo "❌ hyve-api never became healthy" >&2
  docker logs "$CONTAINER_NAME" || true
  exit 1
fi

echo ""
echo "✅ hyve-api running as a plain Docker container ($CONTAINER_NAME), reachable at $PUBLIC_BASE_URL — no Kubernetes Deployment/Pod of its own, and no Kubernetes client of its own at all (--home-cluster=none)."
echo ""
echo "Next steps:"
echo ""
echo "  1. Create a superadmin user (writes directly to the container's own orgdb):"
echo "     docker exec $CONTAINER_NAME hyve cluster-config api create-user <username> --role superadmin --namespace hyve-system --password <password>"
echo ""
echo "  2. Log in:"
echo "     hyve env login --api-url $PUBLIC_BASE_URL"
echo ""
echo "  3. Register '$K3D_CLUSTER' as the reconciling cluster every organization's resources will actually live on:"
echo "     hyve reconciling-cluster create $K3D_CLUSTER --kubeconfig-file $RECONCILING_KUBECONFIG"
echo ""
echo "  4. Create an organization mapped to it:"
echo "     hyve organization create <name> --reconciling-cluster $K3D_CLUSTER"
echo ""
echo "  Re-run this script any time (safe/idempotent) to rebuild the image and recreate the container with your latest local changes — orgdb persists."
