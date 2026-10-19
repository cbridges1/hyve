#!/usr/bin/env bash
# The agent-version test (HYVE-PROPOSAL.md, Decisions): hyve's host
# endpoint against the pinned rancher/rancher-agent. It creates two k3d
# clusters on one Docker network, installs hyve with its host endpoint on
# the first, registers the second as a child (a CAPI Cluster stand-in with
# its kubeconfig Secret), then checks the agent connects and that get,
# exec and port-forward work through /k8s/clusters/<id> as the admin.
#
#   hack/e2e-agent.sh            # builds the image from this checkout
#   AGENT_IMAGE=rancher/rancher-agent:v2.14.4 hack/e2e-agent.sh
#
# Needs docker, k3d, kubectl, openssl, curl and Go. Cleans up on exit.
set -euo pipefail

root="$(cd "$(dirname "$0")/.." && pwd)"
work="$(mktemp -d)"
net=hyve-e2e
mgmt=hyve-e2e-mgmt
child=hyve-e2e-child
port=30443
image=hyve:e2e
agent_image="${AGENT_IMAGE:-}"
export HYVE_HOME="$work/home"

cleanup() {
  k3d cluster delete "$mgmt" "$child" >/dev/null 2>&1 || true
  docker network rm "$net" >/dev/null 2>&1 || true
  rm -rf "$work"
}
trap cleanup EXIT
step() { printf '\n== %s\n' "$*"; }

step "building hyve"
(cd "$root" && go build -o "$work/hyve" .)
docker build -q -t "$image" "$root" >/dev/null

step "creating clusters"
docker network create "$net" >/dev/null 2>&1 || true
k3d cluster create "$mgmt" --network "$net" --no-lb --k3s-arg "--disable=traefik@server:0" --kubeconfig-update-default=false --wait >/dev/null
k3d cluster create "$child" --network "$net" --no-lb --k3s-arg "--disable=traefik@server:0" --kubeconfig-update-default=false --wait >/dev/null
k3d image import -c "$mgmt" "$image" >/dev/null
k3d kubeconfig get "$mgmt" > "$work/mgmt.kubeconfig"
k3d kubeconfig get "$child" | sed -E "s#server: https://[^ ]+#server: https://k3d-$child-server-0:6443#" > "$work/child-internal.kubeconfig"
host="k3d-$mgmt-server-0"

step "making the host's certificate"
cd "$work"
openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:P-256 -nodes -days 1 -subj /CN=e2e-ca -keyout ca.key -out ca.crt 2>/dev/null
openssl req -newkey ec -pkeyopt ec_paramgen_curve:P-256 -nodes -subj /CN=host -keyout tls.key -out tls.csr 2>/dev/null
printf 'subjectAltName=DNS:%s,DNS:localhost,IP:127.0.0.1\nextendedKeyUsage=serverAuth\n' "$host" > ext.cnf
openssl x509 -req -in tls.csr -CA ca.crt -CAkey ca.key -CAcreateserial -days 1 -extfile ext.cnf -out tls.crt 2>/dev/null

step "installing hyve (CAPI and Fleet CRDs only; no providers needed)"
export KUBECONFIG="$work/mgmt.kubeconfig"
curl -sSfL https://github.com/kubernetes-sigs/cluster-api/releases/download/v1.12.11/core-components.yaml \
  | python3 -c '
import sys, yaml
docs = [d for d in yaml.safe_load_all(sys.stdin) if d and d.get("kind") == "CustomResourceDefinition" and d["metadata"]["name"] in ("clusters.cluster.x-k8s.io", "clusterclasses.cluster.x-k8s.io")]
for d in docs:
    d["metadata"].pop("annotations", None); d["spec"].pop("conversion", None)
yaml.safe_dump_all(docs, sys.stdout)' | kubectl apply --server-side -f - >/dev/null
values="$work/values.yaml"
printf 'host:\n  service: {type: NodePort, nodePort: %s}\n' "$port" > "$values"
if [ -n "$agent_image" ]; then printf '  agentImage: %s\n' "$agent_image" >> "$values"; fi
"$work/hyve" install --only-hyve --kubeconfig "$work/mgmt.kubeconfig" --image "$image" --image-pull-policy Never \
  --host-url "https://$host:$port" --host-tls-cert tls.crt --host-tls-key tls.key --host-cacerts ca.crt -f "$values" \
  2>&1 | grep -v '^  waiting' || true
kubectl -n hyve-system rollout status deploy/hyve-host --timeout=120s >/dev/null

step "signing in"
pf_port=18443
kubectl -n hyve-system port-forward svc/hyve-host "$pf_port:80" >/dev/null 2>&1 &
pf=$!
trap 'kill $pf 2>/dev/null; cleanup' EXIT
sleep 3
newpw="$(openssl rand -hex 16)"
{ kubectl -n hyve-system get secret bootstrap-secret -o jsonpath='{.data.bootstrapPassword}' | base64 -d; echo; echo "$newpw"; } \
  | "$work/hyve" login "https://localhost:$pf_port" --username admin --password-stdin --name e2e --ca-cert ca.crt >/dev/null

step "registering the child"
kubectl create namespace e2e >/dev/null
kubectl label namespace e2e hyve.io/environment=true >/dev/null
kubectl -n e2e create secret generic child-kubeconfig --from-file=value="$work/child-internal.kubeconfig" >/dev/null
kubectl apply -f - >/dev/null <<EOF
apiVersion: cluster.x-k8s.io/v1beta2
kind: Cluster
metadata: {name: child, namespace: e2e}
spec:
  clusterNetwork: {pods: {cidrBlocks: [10.42.0.0/16]}}
EOF
kubectl -n e2e patch cluster child --subresource=status --type=merge \
  -p '{"status":{"conditions":[{"type":"Available","status":"True","reason":"E2E","message":"","lastTransitionTime":"2026-01-01T00:00:00Z"}]}}' >/dev/null

step "waiting for the agent to connect"
"$work/hyve" env use e2e >/dev/null
"$work/hyve" cluster kubeconfig child > "$work/child-via-hyve.kubeconfig"
export KUBECONFIG="$work/child-via-hyve.kubeconfig"
for i in $(seq 1 90); do
  if kubectl get nodes >/dev/null 2>&1; then break; fi
  sleep 5
done
kubectl get nodes

step "checks through the tunnel"
test "$(kubectl auth whoami -o jsonpath='{.status.userInfo.username}')" = "hyve:admin"
kubectl run web --image=nginx:alpine --port 80 >/dev/null
kubectl wait --for=condition=Ready pod/web --timeout=180s >/dev/null
test "$(kubectl exec web -- echo ok)" = ok
kubectl port-forward pod/web 18081:80 >/dev/null 2>&1 &
fwd=$!
sleep 3
curl -sf localhost:18081 | grep -q nginx
kill $fwd
echo
echo "PASS: hyve host with ${agent_image:-the pinned rancher-agent}"
