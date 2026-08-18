#!/usr/bin/env bash
set -euo pipefail

# Live smoke test for the MaxConcurrentReconciles fix (see
# internal/controller/reconciler.go, internal/module/executor.go,
# internal/workflow/executor.go, internal/workflow/job_runner.go,
# internal/reconcile/manager.go). Runs `hyve cluster-config controller run`
# against a real Kubernetes cluster (whatever your current kubectl context
# points at) and proves two things:
#
#   1. Concurrency actually happens: N ClusterDefinitions, each with an
#      auth step artificially slowed by SLEEP_SECONDS, complete far faster
#      with --max-concurrent-reconciles > 1 than with =1 (serial).
#   2. No cross-contamination: every cluster ends up with its OWN isolated
#      kubeconfig file (~/.hyve/kubeconfigs/<name>.yaml) containing only
#      its own identity — proving concurrent auth calls never clobber each
#      other via shared process state.
#
# Cleans up everything it creates (controller process, CRs, namespace,
# CRDs it installed, isolated kubeconfig files, scratch dir) on exit,
# success or failure.
#
# Usage: scripts/test-concurrency.sh [max_concurrent] [cluster_count]
#   max_concurrent  --max-concurrent-reconciles value for the "fast" pass (default 3)
#   cluster_count   number of ClusterDefinitions to reconcile (default 3)

MAX_CONCURRENT="${1:-3}"
CLUSTER_COUNT="${2:-3}"
SLEEP_SECONDS=3
NAMESPACE="hyve-concurrency-test"

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WORK_DIR="$(mktemp -d)"
MODULES_DIR="$WORK_DIR/modules"
TIMING_LOG="$WORK_DIR/timing.log"
CONTROLLER_LOG="$WORK_DIR/controller.log"
BIN="$WORK_DIR/hyve"
CONTROLLER_PID=""

CLUSTER_NAMES=()
for i in $(seq 1 "$CLUSTER_COUNT"); do
  CLUSTER_NAMES+=("concurrency-test-$i")
done

# log writes to stderr, not stdout — run_pass's stdout is captured via
# command substitution to get its "elapsed seconds" return value, so any
# progress message printed to stdout would corrupt that value.
log() { echo "── $*" >&2; }

stop_controller() {
  if [ -n "$CONTROLLER_PID" ] && kill -0 "$CONTROLLER_PID" 2>/dev/null; then
    kill "$CONTROLLER_PID" 2>/dev/null || true
    wait "$CONTROLLER_PID" 2>/dev/null || true
  fi
  CONTROLLER_PID=""
}

cleanup() {
  log "Cleaning up"
  stop_controller
  kubectl delete clusterdefinition "${CLUSTER_NAMES[@]}" -n "$NAMESPACE" --ignore-not-found --wait=false >/dev/null 2>&1 || true
  sleep 1
  for c in "${CLUSTER_NAMES[@]}"; do
    kubectl patch clusterdefinition "$c" -n "$NAMESPACE" --type=merge -p '{"metadata":{"finalizers":[]}}' >/dev/null 2>&1 || true
  done
  kubectl delete namespace "$NAMESPACE" --ignore-not-found --wait=false >/dev/null 2>&1 || true
  for c in "${CLUSTER_NAMES[@]}"; do
    rm -f "$HOME/.hyve/kubeconfigs/$c.yaml"
  done
  rm -rf "$WORK_DIR"
}
trap cleanup EXIT

log "Building hyve"
go build -o "$BIN" "$ROOT_DIR"

log "Writing fake authOnly driver module (auth sleeps ${SLEEP_SECONDS}s then writes its own identity to \$KUBECONFIG)"
mkdir -p "$MODULES_DIR/modules/concurrency-test-driver"
cat > "$MODULES_DIR/modules/concurrency-test-driver/module.yaml" <<'EOF'
apiVersion: v1
kind: Module
metadata:
  name: concurrency-test-driver
  version: 0.1.0
  type: authOnly
EOF
cat > "$MODULES_DIR/modules/concurrency-test-driver/auth.yaml" <<EOF
apiVersion: v1
kind: ClusterAuth
metadata:
  name: concurrency-test-driver
spec:
  methods:
    - name: default
      auth:
        script: |
          echo "\$SECONDS START \$HYVE_CLUSTER_NAME" >> "$TIMING_LOG"
          sleep $SLEEP_SECONDS
          echo "cluster=\$HYVE_CLUSTER_NAME" > "\$KUBECONFIG"
          echo "\$SECONDS END \$HYVE_CLUSTER_NAME" >> "$TIMING_LOG"
      exports: KUBECONFIG
EOF

log "Applying CRDs"
kubectl apply -f "$ROOT_DIR/deploy/helm/hyve-controller/crds/hyve.io_clusterdefinitions.yaml" >/dev/null
kubectl apply -f "$ROOT_DIR/deploy/helm/hyve-controller/crds/hyve.io_hyveconfigs.yaml" >/dev/null

kubectl create namespace "$NAMESPACE" >/dev/null 2>&1 || true

create_cluster_definitions() {
  for c in "${CLUSTER_NAMES[@]}"; do
    kubectl apply -n "$NAMESPACE" -f - >/dev/null <<EOF
apiVersion: hyve.io/v1alpha1
kind: ClusterDefinition
metadata:
  name: $c
spec:
  driver:
    source: ./modules/concurrency-test-driver
    version: local
EOF
  done
}

reset_cluster_definitions() {
  # No controller is running at this point (called after stop_controller),
  # so nothing will ever clear the finalizer on its own — force it, then
  # delete, so the next pass starts from a genuinely clean slate. Leaving a
  # deletionTimestamp'd-but-not-yet-removed object around would make the
  # next pass's fresh controller take the *delete* path on startup instead
  # of running auth again, which would break the timing measurement.
  for c in "${CLUSTER_NAMES[@]}"; do
    kubectl patch clusterdefinition "$c" -n "$NAMESPACE" --type=merge -p '{"metadata":{"finalizers":[]}}' >/dev/null 2>&1 || true
  done
  kubectl delete clusterdefinition "${CLUSTER_NAMES[@]}" -n "$NAMESPACE" --ignore-not-found --wait=false >/dev/null 2>&1 || true
  for i in $(seq 1 10); do
    remaining=$(kubectl get clusterdefinition -n "$NAMESPACE" -o name 2>/dev/null | wc -l | tr -d ' ')
    [ "$remaining" -eq 0 ] && break
    sleep 1
  done
}

# run_pass starts the controller with the given concurrency, creates fresh
# ClusterDefinitions, waits for every auth call to finish, and echoes the
# elapsed seconds (via bash's SECONDS builtin, reset per pass).
run_pass() {
  local concurrency="$1" label="$2"
  : > "$TIMING_LOG"
  rm -f "$HOME"/.hyve/kubeconfigs/concurrency-test-*.yaml

  log "[$label] Starting controller (--max-concurrent-reconciles=$concurrency)"
  SECONDS=0
  "$BIN" cluster-config controller run \
    --modules-dir "$MODULES_DIR" \
    --namespace "$NAMESPACE" \
    --max-concurrent-reconciles "$concurrency" \
    > "$CONTROLLER_LOG.$label" 2>&1 &
  CONTROLLER_PID=$!

  for i in $(seq 1 15); do
    grep -q "controller starting" "$CONTROLLER_LOG.$label" 2>/dev/null && break
    sleep 1
  done

  create_cluster_definitions

  local waited=0
  while [ "$waited" -lt 60 ]; do
    # grep -c always prints a count (even "0") when the file exists, so no
    # `|| echo 0` fallback here — that would double-print on a zero match
    # (grep -c exits 1 on zero matches but still writes "0" to stdout).
    count=$(grep -c "END" "$TIMING_LOG" 2>/dev/null)
    [ "$count" -ge "$CLUSTER_COUNT" ] && break
    sleep 1
    waited=$((waited + 1))
  done

  local elapsed=$SECONDS
  stop_controller
  reset_cluster_definitions

  local completed
  completed=$(grep -c "END" "$TIMING_LOG" 2>/dev/null)
  if [ "$completed" -lt "$CLUSTER_COUNT" ]; then
    echo "FAIL [$label]: only $completed/$CLUSTER_COUNT auth calls completed within timeout — see $CONTROLLER_LOG.$label" >&2
    return 1
  fi

  echo "$elapsed"
}

log "=== Pass 1: serial baseline (--max-concurrent-reconciles=1) ==="
serial_elapsed=$(run_pass 1 serial)
log "Serial pass took ${serial_elapsed}s for $CLUSTER_COUNT cluster(s), ${SLEEP_SECONDS}s auth each"

log "=== Pass 2: concurrent (--max-concurrent-reconciles=$MAX_CONCURRENT) ==="
concurrent_elapsed=$(run_pass "$MAX_CONCURRENT" concurrent)
log "Concurrent pass took ${concurrent_elapsed}s for $CLUSTER_COUNT cluster(s), ${SLEEP_SECONDS}s auth each"

echo ""
echo "=== Isolation check (pass 2's kubeconfig files) ==="
ISOLATION_FAIL=0
for c in "${CLUSTER_NAMES[@]}"; do
  f="$HOME/.hyve/kubeconfigs/$c.yaml"
  if [ ! -f "$f" ]; then
    echo "FAIL: $f missing"
    ISOLATION_FAIL=1
    continue
  fi
  content=$(cat "$f")
  if [ "$content" != "cluster=$c" ]; then
    echo "FAIL: $f contains '$content', expected 'cluster=$c' — CROSS-CONTAMINATION"
    ISOLATION_FAIL=1
  else
    echo "OK:   $f -> $content"
  fi
done

echo ""
echo "=== Result ==="
TIMING_FAIL=0
if [ "$concurrent_elapsed" -ge "$serial_elapsed" ]; then
  echo "FAIL: concurrent pass (${concurrent_elapsed}s) was not faster than serial pass (${serial_elapsed}s)"
  TIMING_FAIL=1
else
  echo "OK:   concurrent pass (${concurrent_elapsed}s) faster than serial pass (${serial_elapsed}s) — concurrency confirmed"
fi

if [ "$ISOLATION_FAIL" -eq 0 ] && [ "$TIMING_FAIL" -eq 0 ]; then
  echo ""
  echo "✅ MaxConcurrentReconciles is safe: concurrency helps, and no cluster's kubeconfig leaked into another's."
  exit 0
else
  echo ""
  echo "❌ Test failed — see above."
  exit 1
fi
