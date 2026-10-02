#!/usr/bin/env bash
set -euo pipefail

# Live smoke test for Phase 5 (client-only workflows with cluster-sourced
# secrets — see HYVE-IMPLEMENTATION-PLAN.md). Runs `hyve workflow run`
# against a real Kubernetes cluster (whatever your current kubectl context
# points at) and proves:
#
#   1. A runtime: client workflow's secretsFrom entries resolve into env
#      vars visible to its steps.
#   2. Only the declared keys are exposed — an undeclared key in the same
#      Secret must NOT leak into the step's environment.
#   3. container: set on a runtime: client workflow doesn't block execution
#      (silently ignored, not a validation error) and `hyve workflow
#      validate` surfaces the informational lint warning for it.
#
# Fully isolated from your real hyve state: HYVE_HOME points at a scratch
# directory for this run's SQLite-backed repo registration, so nothing here
# touches ~/.hyve/hyve.db or your real registered repositories. The one
# exception is ~/.hyve/kubeconfigs/<test-cluster>.yaml (module.
# KubeconfigPathForCluster isn't HYVE_HOME-aware — see its own doc comment)
# — cleaned up on exit like everything else.

NAMESPACE="hyve-secretsfrom-test"
TEST_CLUSTER="secretsfrom-test-cluster"
SECRET_NAME="test-credentials"

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WORK_DIR="$(mktemp -d)"
export HYVE_HOME="$WORK_DIR/hyve-home"
REPO_DIR="$WORK_DIR/repo"
BIN="$WORK_DIR/hyve"

log() { echo "── $*" >&2; }

cleanup() {
  log "Cleaning up"
  kubectl delete namespace "$NAMESPACE" --ignore-not-found --wait=false >/dev/null 2>&1 || true
  rm -f "$HOME/.hyve/kubeconfigs/$TEST_CLUSTER.yaml"
  rm -rf "$WORK_DIR"
}
trap cleanup EXIT

log "Building hyve"
go build -o "$BIN" "$ROOT_DIR"

log "Creating test namespace + Secret (two keys, only one declared in secretsFrom)"
kubectl create namespace "$NAMESPACE" >/dev/null 2>&1 || true
kubectl -n "$NAMESPACE" delete secret "$SECRET_NAME" --ignore-not-found >/dev/null 2>&1
kubectl -n "$NAMESPACE" create secret generic "$SECRET_NAME" \
  --from-literal=username=robot-hyve \
  --from-literal=password=super-secret-value \
  --from-literal=undeclared-key=should-never-leak >/dev/null

log "Writing a kubeconfig for '$TEST_CLUSTER' at module.KubeconfigPathForCluster's path"
mkdir -p "$HOME/.hyve/kubeconfigs"
kubectl config view --minify --raw > "$HOME/.hyve/kubeconfigs/$TEST_CLUSTER.yaml"

log "Registering a scratch repo (HYVE_HOME=$HYVE_HOME, isolated from your real hyve state)"
mkdir -p "$REPO_DIR/workflows"
"$BIN" env create --path "$REPO_DIR" >/dev/null

OUT_FILE="$WORK_DIR/step-output.txt"
cat > "$REPO_DIR/workflows/secrets-test.yaml" <<EOF
apiVersion: hyve.io/v1alpha1
kind: Workflow
metadata:
  name: secrets-test
spec:
  runtime: client
  secretsFrom:
    - cluster: $TEST_CLUSTER
      namespace: $NAMESPACE
      secretRef: $SECRET_NAME
      keys:
        - key: username
          env: TEST_USERNAME
        - key: password
          env: TEST_PASSWORD
  jobs:
    - name: check
      container: this-image-does-not-exist:latest
      steps:
        - name: dump-env
          command: |
            echo "USERNAME=\$TEST_USERNAME" > $OUT_FILE
            echo "PASSWORD=\$TEST_PASSWORD" >> $OUT_FILE
            echo "UNDECLARED=\${UNDECLARED_KEY:-<absent>}" >> $OUT_FILE
EOF

log "Running: hyve workflow run secrets-test"
if ! "$BIN" workflow run secrets-test > "$WORK_DIR/run.log" 2>&1; then
  echo "FAIL: hyve workflow run exited non-zero — log:" >&2
  cat "$WORK_DIR/run.log" >&2
  exit 1
fi

echo ""
echo "=== Resolved env in the step ==="
FAIL=0
if [ ! -f "$OUT_FILE" ]; then
  echo "FAIL: step never ran (no output file) — see $WORK_DIR/run.log"
  FAIL=1
else
  cat "$OUT_FILE"
  grep -q "^USERNAME=robot-hyve$" "$OUT_FILE" && echo "OK:   TEST_USERNAME resolved correctly" || { echo "FAIL: TEST_USERNAME missing/wrong"; FAIL=1; }
  grep -q "^PASSWORD=super-secret-value$" "$OUT_FILE" && echo "OK:   TEST_PASSWORD resolved correctly" || { echo "FAIL: TEST_PASSWORD missing/wrong"; FAIL=1; }
  grep -q "^UNDECLARED=<absent>$" "$OUT_FILE" && echo "OK:   undeclared secret key did not leak into the step's env" || { echo "FAIL: undeclared-key leaked into the step's env"; FAIL=1; }
fi

echo ""
echo "=== container: ignored, not rejected (runtime: client) ==="
if grep -q "this-image-does-not-exist" "$WORK_DIR/run.log"; then
  echo "FAIL: run.log references the bogus container image — container: should have been silently ignored"
  FAIL=1
else
  echo "OK:   workflow completed without ever touching the (deliberately bogus) container: image"
fi

echo ""
echo "=== hyve workflow validate: lint warning for container: on runtime: client ==="
VALIDATE_OUT="$("$BIN" workflow validate secrets-test 2>&1 || true)"
echo "$VALIDATE_OUT"
if echo "$VALIDATE_OUT" | grep -qi "runtime: client"; then
  echo "OK:   validate surfaced the container:-has-no-effect lint warning"
else
  echo "FAIL: expected lint warning about container: on runtime: client not found"
  FAIL=1
fi

echo ""
if [ "$FAIL" -eq 0 ]; then
  echo "✅ Phase 5 secretsFrom + runtime: client verified end-to-end."
  exit 0
else
  echo "❌ Test failed — see above."
  exit 1
fi
