#!/usr/bin/env bash
# Fetches the pinned upstream manifests `hyve install` (and local mode)
# embed: cert-manager and the Cluster API Operator. Bump a version here and
# rerun; then test an install. CAPI core and CAAPF are installed by the
# operator from these pins (internal/install/install.go), and Fleet by CAAPF.
set -euo pipefail
CERT_MANAGER=v1.20.4
CAPI_OPERATOR=v0.29.0
# Local mode's lean install applies these directly (no operator, no
# cert-manager); keep them in step with internal/install.CAPIVersion and
# CAAPFVersion.
CAPI=v1.12.11
CAAPF=v0.15.1
dir="$(cd "$(dirname "$0")/.." && pwd)/internal/install/manifests"
mkdir -p "$dir"
curl -sSfL -o "$dir/cert-manager.yaml" "https://github.com/cert-manager/cert-manager/releases/download/${CERT_MANAGER}/cert-manager.yaml"
curl -sSfL -o "$dir/capi-operator.yaml" "https://github.com/kubernetes-sigs/cluster-api-operator/releases/download/${CAPI_OPERATOR}/operator-components.yaml"
mkdir -p "$dir/lean"
for f in core-components bootstrap-components control-plane-components infrastructure-components-development; do
  curl -sSfL -o "$dir/lean/$f.yaml" "https://github.com/kubernetes-sigs/cluster-api/releases/download/${CAPI}/$f.yaml"
done
curl -sSfL -o "$dir/lean/addon-components.yaml" "https://github.com/rancher/cluster-api-addon-provider-fleet/releases/download/${CAAPF}/addon-components.yaml"
printf 'cert-manager %s\ncapi-operator %s\ncapi %s\ncaapf %s\n' "$CERT_MANAGER" "$CAPI_OPERATOR" "$CAPI" "$CAAPF" > "$dir/VERSIONS"
