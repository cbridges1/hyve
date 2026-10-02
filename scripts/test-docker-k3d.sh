#!/usr/bin/env bash
set -euo pipefail

# Milestone 11 (HYVE-ORGANIZATION-MODEL-IMPLEMENTATION-PLAN.md,
# nexus-config/docs): automated, repeatable proof of Milestone 10's own
# central claim — hyve's control plane doesn't need to run on any
# Kubernetes cluster of its own — one step further than that milestone's
# own manual verification, which flipped --home-cluster=none on a pod
# that was still running *inside* a Kubernetes Deployment. Here, hyve-api
# and hyve-controller run as plain `docker run` containers on the host —
# no Kubernetes Deployment/Pod for either process at all — and a
# disposable k3d cluster, reached purely over the network as a registered
# Milestone 6 reconciling cluster, is the only place any real
# infrastructure actually gets reconciled.
#
# Precise about what this does and doesn't prove (see the milestone's own
# section for the full reasoning): hyve-api achieves genuinely zero
# Kubernetes client of its own (--home-cluster=none) — every
# organization's resources route to the registered k3d reconciling
# cluster. hyve-controller has no equivalent remote-routing mechanism
# today — its own "home" cluster IS the k3d cluster here, reached over
# the network via KUBECONFIG from a container that isn't deployed on it,
# not a Kubernetes-agnostic remote-routing capability the way hyve-api's
# resourceClient is. This test deliberately doesn't paper over that
# asymmetry.
#
# Cleans up everything it creates on exit, success or failure.

CLUSTER_NAME="hyve-m11-test"
ORG_NAME="m11org"
API_CONTAINER="hyve-m11-api"
CONTROLLER_CONTAINER="hyve-m11-controller"
ORGDB_VOLUME="hyve-m11-orgdb"
API_PORT="${HYVE_M11_API_PORT:-18090}"
IMAGE="hyve:m11test"
# Passed explicitly to every docker build/run below, not left to Docker's
# own default resolution — some environments carry a broken
# DOCKER_DEFAULT_PLATFORM (confirmed live: one value observed was the
# literal string "linux/amd64export", not a valid platform at all),
# which silently sends `docker run` down a registry-pull path instead of
# using the image `docker build --load` already put in the local store,
# failing with a misleading "pull access denied" error that has nothing
# to do with the real problem.
DOCKER_PLATFORM="linux/$(docker version --format '{{.Server.Arch}}')"

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WORK_DIR="$(mktemp -d)"
MODULES_DIR="$WORK_DIR/modules"
mkdir -p "$MODULES_DIR"

log() { echo "── $*" >&2; }

cleanup() {
  log "Cleaning up"
  docker rm -f "$API_CONTAINER" "$CONTROLLER_CONTAINER" >/dev/null 2>&1 || true
  docker volume rm "$ORGDB_VOLUME" >/dev/null 2>&1 || true
  docker rmi "$IMAGE" >/dev/null 2>&1 || true
  k3d cluster delete "$CLUSTER_NAME" >/dev/null 2>&1 || true
  rm -rf "$WORK_DIR"
}
trap cleanup EXIT

log "Creating disposable k3d cluster $CLUSTER_NAME"
k3d cluster create "$CLUSTER_NAME" --wait >/dev/null

log "Applying hyve CRDs onto it"
kubectl --context "k3d-$CLUSTER_NAME" apply -f "$ROOT_DIR/deploy/helm/hyve/crds/" >/dev/null

log "Building the hyve image ($IMAGE) — the same deploy/Dockerfile.dev scripts/install-local.sh already builds as hyve:dev, built from this checkout's own local source"
docker build --platform "$DOCKER_PLATFORM" --load -f "$ROOT_DIR/deploy/Dockerfile.dev" -t "$IMAGE" "$ROOT_DIR" >/dev/null

# host.docker.internal + insecure-skip-tls-verify: the same cross-cluster
# reachability trick this plan's own live verification already
# established for one k3d cluster reaching another — needed here because
# a container on the Docker default bridge network can't resolve the k3d
# cluster's own internal Docker network hostname, and that cluster's own
# serving certificate has no SAN for host.docker.internal.
SERVERLB_PORT=$(docker port "k3d-$CLUSTER_NAME-serverlb" 6443/tcp | head -1 | grep -oE '[0-9]+$')
if [ -z "$SERVERLB_PORT" ]; then
  log "❌ Could not determine k3d-$CLUSTER_NAME-serverlb's own mapped host port for 6443/tcp"
  exit 1
fi
log "k3d API server reachable from containers at host.docker.internal:$SERVERLB_PORT"

KUBECONFIG_RAW="$WORK_DIR/kubeconfig-raw.yaml"
KUBECONFIG_REWRITTEN="$WORK_DIR/kubeconfig.yaml"
CLUSTER_CA="$WORK_DIR/ca.crt"
k3d kubeconfig get "$CLUSTER_NAME" > "$KUBECONFIG_RAW"
python3 - "$KUBECONFIG_RAW" "$KUBECONFIG_REWRITTEN" "$SERVERLB_PORT" "$CLUSTER_CA" <<'PYEOF'
import base64, sys, yaml
src, dst, port, ca_out = sys.argv[1], sys.argv[2], sys.argv[3], sys.argv[4]
with open(src) as f:
    doc = yaml.safe_load(f)
for cluster in doc.get("clusters", []):
    # The real cluster CA, extracted before it's stripped below — the
    # controller's own host-cluster-kubeconfig-minting path (internal/
    # reconcile/host.go, exercised even for a driver-less "primary"
    # cluster) reads --in-cluster-ca-path unconditionally; a bare Docker
    # container has no Kubernetes-auto-mounted ServiceAccount CA file the
    # way a real pod would, so this script supplies the genuine article
    # explicitly instead.
    ca_data = cluster["cluster"].get("certificate-authority-data")
    if ca_data:
        with open(ca_out, "wb") as f:
            f.write(base64.b64decode(ca_data))
    cluster["cluster"]["server"] = f"https://host.docker.internal:{port}"
    cluster["cluster"]["insecure-skip-tls-verify"] = True
    cluster["cluster"].pop("certificate-authority-data", None)
with open(dst, "w") as f:
    yaml.safe_dump(doc, f)
PYEOF
if [ ! -s "$CLUSTER_CA" ]; then
  log "❌ Could not extract the k3d cluster's own CA certificate from its kubeconfig"
  exit 1
fi

log "Starting hyve-api (--home-cluster=none, --db=sqlite, orgdb on a named Docker volume — the first real use of a *persistent* orgdb volume in this repo, sidestepping the Kubernetes-PVC-specific gap Milestone 7 closed a different way)"
docker run -d --name "$API_CONTAINER" --platform "$DOCKER_PLATFORM" \
  -v "$ORGDB_VOLUME:/data" \
  -v "$MODULES_DIR:/var/lib/hyve/modules:ro" \
  -p "$API_PORT:8090" \
  "$IMAGE" \
  cluster-config api run \
    --namespace=hyve-system \
    --db=sqlite --db-dsn=/data/orgdb.sqlite \
    --home-cluster=none \
    --modules-dir=/var/lib/hyve/modules \
    --bind-address=:8090 \
    --public-base-url="http://localhost:$API_PORT" >/dev/null

log "Waiting for hyve-api to become healthy"
API_HEALTHY=""
for _ in $(seq 1 30); do
  if curl -sf "http://localhost:$API_PORT/healthz" >/dev/null 2>&1; then
    API_HEALTHY=1
    break
  fi
  sleep 1
done
if [ -z "$API_HEALTHY" ]; then
  log "❌ hyve-api never became healthy"
  docker logs "$API_CONTAINER" || true
  exit 1
fi

log "Confirming hyve-api genuinely started with no Kubernetes client of its own"
if ! docker logs "$API_CONTAINER" 2>&1 | grep -q "home-cluster=none"; then
  log "❌ expected --home-cluster=none startup log line not found"
  docker logs "$API_CONTAINER" || true
  exit 1
fi

log "Bootstrapping a superadmin account"
PASSWORD=$(openssl rand -base64 18)
docker exec "$API_CONTAINER" hyve cluster-config api create-user m11admin \
  --role superadmin --namespace hyve-system --password "$PASSWORD" --db-dsn /data/orgdb.sqlite >/dev/null

log "Logging in"
LOGIN_RESP="$WORK_DIR/login.json"
curl -sf -X POST "http://localhost:$API_PORT/auth/login" -H "Content-Type: application/json" \
  -d "{\"username\":\"m11admin\",\"password\":\"$PASSWORD\"}" > "$LOGIN_RESP"
TOKEN=$(python3 -c "import json;print(json.load(open('$LOGIN_RESP'))['accessToken'])")

log "Registering the k3d cluster as a reconciling cluster"
REGISTER_BODY="$WORK_DIR/register.json"
python3 -c "
import json
print(json.dumps({'name': 'cell-m11', 'kubeconfig': open('$KUBECONFIG_REWRITTEN').read()}))
" > "$REGISTER_BODY"
curl -sf -X POST "http://localhost:$API_PORT/api/reconciling-clusters" \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d @"$REGISTER_BODY" >/dev/null

log "Creating organization $ORG_NAME, mapped to it"
curl -sf -X POST "http://localhost:$API_PORT/api/organizations" \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d "{\"name\":\"$ORG_NAME\",\"reconcilingCluster\":\"cell-m11\"}" >/dev/null

log "Confirming the organization's namespace landed on k3d, not any home cluster hyve-api doesn't have"
if ! kubectl --context "k3d-$CLUSTER_NAME" get namespace "$ORG_NAME" >/dev/null 2>&1; then
  log "❌ organization namespace never appeared on k3d-$CLUSTER_NAME"
  exit 1
fi

log "Provisioning hyve-host-admin (ServiceAccount + ClusterRoleBinding, matching deploy/helm/hyve/templates/api-access-roles.yaml's own shape) — a real Helm-based install always creates this, but this test never runs Helm against the reconciling cluster, only registers its kubeconfig"
kubectl --context "k3d-$CLUSTER_NAME" apply -f - >/dev/null <<EOF
apiVersion: v1
kind: ServiceAccount
metadata:
  name: hyve-host-admin
  namespace: $ORG_NAME
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: hyve-host-admin-$ORG_NAME
subjects:
  - kind: ServiceAccount
    name: hyve-host-admin
    namespace: $ORG_NAME
roleRef:
  kind: ClusterRole
  name: cluster-admin
  apiGroup: rbac.authorization.k8s.io
EOF

log "Creating a driver-less ClusterDefinition (spec.access.method: primary — a real, supported zero-config shape needing no module resolution at all, see internal/reconcile/manager.go's own isHostClusterWithoutDriver)"
curl -sf -X POST "http://localhost:$API_PORT/api/clusters" \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -H "X-Hyve-Act-As-Namespace: $ORG_NAME" \
  -d '{"name":"web","spec":{"region":"local","access":{"method":"primary"}}}' >/dev/null

# resolveAddressedName's own convention (Milestone 3): a cluster named
# "web" in an organization's "default" environment is really the
# Kubernetes object "default-web".
CD_NAME="default-web"

log "Starting hyve-controller, KUBECONFIG pointed directly at k3d (its own 'home' here — see this script's own header comment on why that's not the same as hyve-api's own remote routing)"
docker run -d --name "$CONTROLLER_CONTAINER" --platform "$DOCKER_PLATFORM" \
  -v "$KUBECONFIG_REWRITTEN:/kubeconfig/config:ro" \
  -v "$MODULES_DIR:/var/lib/hyve/modules:ro" \
  -v "$CLUSTER_CA:/kubeconfig/ca.crt:ro" \
  -e KUBECONFIG=/kubeconfig/config \
  "$IMAGE" \
  cluster-config controller run \
    --namespace="$ORG_NAME" \
    --modules-dir=/var/lib/hyve/modules \
    --in-cluster-ca-path=/kubeconfig/ca.crt >/dev/null

log "Polling for hyve-controller to reconcile $CD_NAME to Ready"
READY=""
for _ in $(seq 1 60); do
  STATUS=$(kubectl --context "k3d-$CLUSTER_NAME" get clusterdefinition "$CD_NAME" -n "$ORG_NAME" \
    -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}' 2>/dev/null || true)
  if [ "$STATUS" = "True" ]; then
    READY=1
    break
  fi
  sleep 2
done

if [ -z "$READY" ]; then
  log "❌ $CD_NAME never reached Ready"
  kubectl --context "k3d-$CLUSTER_NAME" get clusterdefinition "$CD_NAME" -n "$ORG_NAME" -o yaml || true
  log "hyve-controller logs:"
  docker logs "$CONTROLLER_CONTAINER" || true
  exit 1
fi

log "✅ hyve-api and hyve-controller, both plain Docker containers with no Kubernetes Deployment/Pod of their own, drove real infrastructure on a k3d cluster reached purely as a registered reconciling cluster — hyve-api with zero Kubernetes client of its own the entire time"
