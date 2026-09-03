#!/usr/bin/env bash
set -euo pipefail

# Live verification that two hyve installs (tenants) can safely share one
# cluster — see HYVE-MULTI-TENANCY-PLAN.md. Installs the chart twice, into
# two disposable namespaces, and proves via kubectl auth can-i (real RBAC
# decisions, not application-level assertions) that:
#
#   1. Two installs coexist with no ownership/name conflicts.
#   2. Tenant A's API ServiceAccount cannot list/read tenant B's
#      HyveAccessBinding objects (the fix for HyveAccessBinding being
#      cluster-scoped).
#   3. Tenant A's default admin ServiceAccount cannot touch tenant B's
#      namespace at all (the fix for the cluster-admin-via-proxy
#      escalation) — while it CAN still act within its own namespace.
#
# Deliberately does not build an image or wait for pods to become ready —
# RBAC objects (ServiceAccount/Role/RoleBinding) are created as part of
# `helm install` regardless of whether the pods it also creates ever go
# healthy, and `kubectl auth can-i --as=<ServiceAccount>` evaluates purely
# against those RBAC objects with no running pod involved. A placeholder
# image keeps this fast and independent of scripts/install-local.sh's
# Docker build step.
#
# REQUIRES a cluster already running the namespaced HyveAccessBinding CRD
# (see this repo's CRD migration note) — CustomResourceDefinition.spec.scope
# is immutable, so "Applying CRDs" below fails on a cluster still holding
# the older cluster-scoped version until that one-time migration has run.
#
# Cleans up everything it creates on exit, success or failure.

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SUFFIX="$(date +%s)"
NS_A="hyve-mt-test-a-$SUFFIX"
NS_B="hyve-mt-test-b-$SUFFIX"
RELEASE_A="hyve-test-a-$SUFFIX"
RELEASE_B="hyve-test-b-$SUFFIX"
FAIL=0

log() { echo "── $*" >&2; }

cleanup() {
  log "Cleaning up"
  helm uninstall "$RELEASE_A" -n "$NS_A" >/dev/null 2>&1 || true
  helm uninstall "$RELEASE_B" -n "$NS_B" >/dev/null 2>&1 || true
  kubectl delete namespace "$NS_A" "$NS_B" --ignore-not-found --wait=false >/dev/null 2>&1 || true
}
trap cleanup EXIT

log "Applying CRDs"
kubectl apply -f "$ROOT_DIR/deploy/helm/hyve/crds/" >/dev/null

log "Creating namespaces $NS_A and $NS_B"
kubectl create namespace "$NS_A" >/dev/null
kubectl create namespace "$NS_B" >/dev/null

# A placeholder image — never expected to actually run, only to exist as a
# value the chart's templates can render. --wait is omitted deliberately
# (see header comment): this script never waits on pod health.
install_tenant() {
  local release="$1" ns="$2"
  helm install "$release" "$ROOT_DIR/deploy/helm/hyve" \
    --namespace "$ns" \
    --set image.repository=hyve --set image.tag=dev \
    --set namespace="$ns" >/dev/null
}

log "Installing tenant A ($RELEASE_A in $NS_A)"
install_tenant "$RELEASE_A" "$NS_A"

log "Installing tenant B ($RELEASE_B in $NS_B) — proves no name/ownership conflict with tenant A"
install_tenant "$RELEASE_B" "$NS_B"
echo "OK:   both installs succeeded with no conflicts"

check() {
  local desc="$1" expect="$2"; shift 2
  local got
  got=$(kubectl auth can-i "$@" 2>/dev/null || true)
  if [ "$got" = "$expect" ]; then
    echo "OK:   $desc -> $got"
  else
    echo "FAIL: $desc -> expected $expect, got $got"
    FAIL=1
  fi
}

echo ""
echo "=== HyveAccessBinding: no cross-namespace visibility ==="
check "tenant A's API SA can list HyveAccessBindings in its own namespace" "yes" \
  list hyveaccessbindings -n "$NS_A" --as="system:serviceaccount:$NS_A:hyve-api"
check "tenant A's API SA can list HyveAccessBindings in tenant B's namespace" "no" \
  list hyveaccessbindings -n "$NS_B" --as="system:serviceaccount:$NS_A:hyve-api"
check "tenant B's API SA can list HyveAccessBindings in tenant A's namespace" "no" \
  list hyveaccessbindings -n "$NS_A" --as="system:serviceaccount:$NS_B:hyve-api"

echo ""
echo "=== Default admin role: namespace-scoped, not cluster-admin ==="
check "tenant A's admin SA can get secrets in its own namespace" "yes" \
  get secrets -n "$NS_A" --as="system:serviceaccount:$NS_A:hyve-access-admin"
check "tenant A's admin SA CANNOT get secrets in tenant B's namespace" "no" \
  get secrets -n "$NS_B" --as="system:serviceaccount:$NS_A:hyve-access-admin"
check "tenant A's admin SA CANNOT list nodes (cluster-scoped resource)" "no" \
  list nodes --as="system:serviceaccount:$NS_A:hyve-access-admin"

echo ""
if [ "$FAIL" -eq 0 ]; then
  echo "✅ Multi-tenant namespace isolation verified end-to-end (RBAC, both binding scope and role scope)."
  exit 0
else
  echo "❌ Test failed — see above."
  exit 1
fi
