#!/usr/bin/env bash
set -euo pipefail

# Live smoke test for Phase 6's API + auth layer (see
# HYVE-CONTROLLER-ARCHITECTURE-PLAN.md's Phase 6.1-6.5a). Runs `hyve api
# run` as a local process against a real Kubernetes cluster (whatever your
# current kubectl context points at) and proves:
#
#   1. Local (username/password) login works: wrong password and unknown
#      username are both rejected identically, correct credentials issue a
#      bearer token.
#   2. Every /api/* route requires that token (401 without it).
#   3. Role gating is real: a read-only user gets 403 on POST/DELETE
#      /api/clusters but 200 on GET; an admin user gets through.
#   4. /api/clusters CRUD round-trips against real ClusterDefinition CRs.
#   5. GET /api/kubeconfig?cluster=<name> exercises the full
#      ModuleAuthProvider path — resolves the driver module, runs its
#      auth.yaml, and returns the resulting kubeconfig.
#
# NOT covered here (needs a real in-cluster deployment, not a local
# process): the primary-cluster kubeconfig path and /proxy/*, both of
# which need a real in-cluster CA file — see run.go's own warning when
# that file isn't found.
#
# Cleans up everything it creates on exit, success or failure.

NAMESPACE="hyve-api-test"
MODULE_CLUSTER="api-test-module-auth-cluster"

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WORK_DIR="$(mktemp -d)"
MODULES_DIR="$WORK_DIR/modules"
BIN="$WORK_DIR/hyve"
API_LOG="$WORK_DIR/api.log"
API_PID=""

log() { echo "── $*" >&2; }

stop_api() {
  if [ -n "$API_PID" ] && kill -0 "$API_PID" 2>/dev/null; then
    kill "$API_PID" 2>/dev/null || true
    wait "$API_PID" 2>/dev/null || true
  fi
  API_PID=""
}

cleanup() {
  log "Cleaning up"
  stop_api
  kubectl delete clusterdefinition "$MODULE_CLUSTER" -n "$NAMESPACE" --ignore-not-found --wait=false >/dev/null 2>&1 || true
  kubectl delete hyveaccessbinding admin-test-binding readonly-test-binding --ignore-not-found >/dev/null 2>&1 || true
  kubectl delete namespace "$NAMESPACE" --ignore-not-found --wait=false >/dev/null 2>&1 || true
  rm -f "$HOME/.hyve/kubeconfigs/$MODULE_CLUSTER.yaml"
  rm -rf "$WORK_DIR"
}
trap cleanup EXIT

log "Building hyve"
go build -o "$BIN" "$ROOT_DIR"

log "Applying CRDs"
kubectl apply -f "$ROOT_DIR/deploy/crds/hyve.io_clusterdefinitions.yaml" >/dev/null
kubectl apply -f "$ROOT_DIR/deploy/crds/hyve.io_hyveaccessbindings.yaml" >/dev/null

kubectl create namespace "$NAMESPACE" >/dev/null 2>&1 || true

log "Creating hyve-api-credentials Secret (session-signing-key)"
kubectl -n "$NAMESPACE" delete secret hyve-api-credentials --ignore-not-found >/dev/null 2>&1
kubectl -n "$NAMESPACE" create secret generic hyve-api-credentials \
  --from-literal=session-signing-key="$(openssl rand -hex 32)" >/dev/null

log "Creating admin + read-only test users"
"$BIN" api create-user admin-user --role admin --namespace "$NAMESPACE" \
  --binding-name admin-test-binding --password 'admin-pass-123' | kubectl apply -f - >/dev/null
"$BIN" api create-user readonly-user --role read-only --namespace "$NAMESPACE" \
  --binding-name readonly-test-binding --password 'readonly-pass-123' | kubectl apply -f - >/dev/null

log "Writing fake authOnly driver module for the kubeconfig test"
mkdir -p "$MODULES_DIR/modules/fake-driver"
cat > "$MODULES_DIR/modules/fake-driver/module.yaml" <<'EOF'
apiVersion: v1
kind: Module
metadata:
  name: fake-driver
  version: 0.1.0
  type: authOnly
EOF
cat > "$MODULES_DIR/modules/fake-driver/auth.yaml" <<'EOF'
apiVersion: v1
kind: ClusterAuth
metadata:
  name: fake-driver
spec:
  methods:
    - name: default
      auth:
        script: "echo cluster=$HYVE_CLUSTER_NAME > \"$KUBECONFIG\""
      exports: KUBECONFIG
EOF

log "Starting hyve api run"
"$BIN" api run --namespace "$NAMESPACE" --modules-dir "$MODULES_DIR" --bind-address ":18090" \
  > "$API_LOG" 2>&1 &
API_PID=$!
for i in $(seq 1 15); do
  grep -q "hyve api starting" "$API_LOG" 2>/dev/null && break
  sleep 1
done

BASE="http://127.0.0.1:18090"
FAIL=0

echo ""
echo "=== 1. Login ==="
wrong_resp=$(curl -s -o /dev/null -w "%{http_code}" -X POST "$BASE/auth/login" -d '{"username":"admin-user","password":"wrong"}')
unknown_resp=$(curl -s -o /dev/null -w "%{http_code}" -X POST "$BASE/auth/login" -d '{"username":"nobody","password":"whatever"}')
if [ "$wrong_resp" = "401" ] && [ "$unknown_resp" = "401" ]; then
  echo "OK:   wrong password (401) and unknown user (401) both rejected"
else
  echo "FAIL: expected 401/401, got wrong=$wrong_resp unknown=$unknown_resp"; FAIL=1
fi

ADMIN_TOKEN=$(curl -s -X POST "$BASE/auth/login" -d '{"username":"admin-user","password":"admin-pass-123"}' | jq -r '.token')
READONLY_TOKEN=$(curl -s -X POST "$BASE/auth/login" -d '{"username":"readonly-user","password":"readonly-pass-123"}' | jq -r '.token')
if [ -n "$ADMIN_TOKEN" ] && [ -n "$READONLY_TOKEN" ]; then
  echo "OK:   admin and read-only tokens issued"
else
  echo "FAIL: could not obtain tokens"; FAIL=1
fi

echo ""
echo "=== 2. Auth required on /api/* ==="
noauth_resp=$(curl -s -o /dev/null -w "%{http_code}" "$BASE/api/clusters")
if [ "$noauth_resp" = "401" ]; then
  echo "OK:   GET /api/clusters with no token -> 401"
else
  echo "FAIL: expected 401, got $noauth_resp"; FAIL=1
fi

echo ""
echo "=== 3. Role gating ==="
ro_create_resp=$(curl -s -o /dev/null -w "%{http_code}" -X POST "$BASE/api/clusters" \
  -H "Authorization: Bearer $READONLY_TOKEN" -d "{\"name\":\"$MODULE_CLUSTER\"}")
if [ "$ro_create_resp" = "403" ]; then
  echo "OK:   read-only POST /api/clusters -> 403"
else
  echo "FAIL: expected 403, got $ro_create_resp"; FAIL=1
fi

ro_list_resp=$(curl -s -o /dev/null -w "%{http_code}" "$BASE/api/clusters" -H "Authorization: Bearer $READONLY_TOKEN")
if [ "$ro_list_resp" = "200" ]; then
  echo "OK:   read-only GET /api/clusters -> 200"
else
  echo "FAIL: expected 200, got $ro_list_resp"; FAIL=1
fi

echo ""
echo "=== 4. Cluster CRUD (admin) ==="
create_body="{\"name\":\"$MODULE_CLUSTER\",\"spec\":{\"driver\":{\"source\":\"./modules/fake-driver\",\"version\":\"local\"}}}"
create_resp=$(curl -s -o /dev/null -w "%{http_code}" -X POST "$BASE/api/clusters" \
  -H "Authorization: Bearer $ADMIN_TOKEN" -d "$create_body")
if [ "$create_resp" = "201" ]; then
  echo "OK:   admin POST /api/clusters -> 201"
else
  echo "FAIL: expected 201, got $create_resp"; FAIL=1
fi

get_body=$(curl -s "$BASE/api/clusters/$MODULE_CLUSTER" -H "Authorization: Bearer $ADMIN_TOKEN")
if echo "$get_body" | grep -q "$MODULE_CLUSTER"; then
  echo "OK:   GET /api/clusters/$MODULE_CLUSTER returns the created cluster"
else
  echo "FAIL: unexpected body: $get_body"; FAIL=1
fi

echo ""
echo "=== 5. GET /api/kubeconfig (ModuleAuthProvider path) ==="
kc_body=$(curl -s "$BASE/api/kubeconfig?cluster=$MODULE_CLUSTER" -H "Authorization: Bearer $ADMIN_TOKEN")
if echo "$kc_body" | grep -q "cluster=$MODULE_CLUSTER"; then
  echo "OK:   kubeconfig resolved via the module's auth.yaml: $kc_body"
else
  echo "FAIL: unexpected kubeconfig body: $kc_body"; FAIL=1
fi

missing_resp=$(curl -s -o /dev/null -w "%{http_code}" "$BASE/api/kubeconfig?cluster=does-not-exist" -H "Authorization: Bearer $ADMIN_TOKEN")
if [ "$missing_resp" = "404" ]; then
  echo "OK:   kubeconfig for unknown cluster -> 404"
else
  echo "FAIL: expected 404, got $missing_resp"; FAIL=1
fi

echo ""
echo "=== 6. Delete (admin) ==="
ro_delete_resp=$(curl -s -o /dev/null -w "%{http_code}" -X DELETE "$BASE/api/clusters/$MODULE_CLUSTER" -H "Authorization: Bearer $READONLY_TOKEN")
delete_resp=$(curl -s -o /dev/null -w "%{http_code}" -X DELETE "$BASE/api/clusters/$MODULE_CLUSTER" -H "Authorization: Bearer $ADMIN_TOKEN")
if [ "$ro_delete_resp" = "403" ] && [ "$delete_resp" = "204" ]; then
  echo "OK:   read-only DELETE -> 403, admin DELETE -> 204"
else
  echo "FAIL: expected 403/204, got ro=$ro_delete_resp admin=$delete_resp"; FAIL=1
fi

echo ""
if [ "$FAIL" -eq 0 ]; then
  echo "✅ Phase 6 API + auth verified end-to-end (login, authn, authz, cluster CRUD, module-auth kubeconfig)."
  exit 0
else
  echo "❌ Test failed — see above. API log:"
  cat "$API_LOG"
  exit 1
fi
