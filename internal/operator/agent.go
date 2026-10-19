package operator

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/yaml"

	"github.com/cbridges1/hyve/internal/clusters"
	"github.com/cbridges1/hyve/internal/kube"
	"github.com/cbridges1/hyve/internal/tunnel"
)

// DefaultAgentImage is the cattle-cluster-agent hyve's host endpoint is
// tested against. It moves only after the tests pass on a new version
// (HYVE-PROPOSAL.md, Decisions).
const DefaultAgentImage = "rancher/rancher-agent:v2.14.3"

// annotAgentManifest on an agent's Secret: the hash of the manifest last
// applied to its cluster.
const annotAgentManifest = "hyve.io/agent-manifest"

// AgentOptions configure agent registration, for installs where hyve is
// the Rancher host.
type AgentOptions struct {
	// HostURL is the host endpoint's address as child clusters reach it
	// (CATTLE_SERVER). Empty turns agent registration off: with Rancher,
	// Turtles and Rancher install the agent.
	HostURL string
	// CACerts is the PEM CA the host's certificate chains to, when it
	// isn't publicly trusted; agents pin its checksum.
	CACerts string
	// Image is the cattle-cluster-agent image.
	Image string
	// Namespace holds the agents' Secrets (the host's namespace).
	Namespace string
	// AdminUser is the hyve user who is cluster-admin on every child, as
	// Rancher's admin is.
	AdminUser string
}

// Agents installs cattle-cluster-agent on each available CAPI cluster,
// pointed at hyve's host endpoint, and forgets a cluster's agent when the
// cluster goes.
type Agents struct {
	Client   client.Client
	Registry *tunnel.Registry
	Options  AgentOptions
	// NewChildClient builds a client for a child from its kubeconfig;
	// replaced in tests.
	NewChildClient func(kubeconfig []byte) (Applier, error)
}

// Applier applies objects to a child cluster.
type Applier interface {
	ApplyObject(ctx context.Context, o *unstructured.Unstructured) error
}

func defaultChildClient(kubeconfig []byte) (Applier, error) {
	cfg, err := clientcmd.RESTConfigFromKubeConfig(kubeconfig)
	if err != nil {
		return nil, err
	}
	cfg.Timeout = 30 * time.Second
	return kube.New(cfg)
}

// Reconcile implements reconcile.Reconciler.
func (r *Agents) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	lg := log.FromContext(ctx).WithValues("cluster", req.NamespacedName)
	id := tunnel.ClusterID(req.Namespace, req.Name)
	c := object(clusterGVK)
	err := r.Client.Get(ctx, req.NamespacedName, c)
	if apierrors.IsNotFound(err) {
		return reconcile.Result{}, r.Registry.Delete(ctx, id)
	}
	if err != nil {
		return reconcile.Result{}, err
	}
	if c.GetDeletionTimestamp() != nil {
		return reconcile.Result{}, nil
	}
	if clusters.ConditionStatus(c, "ControlPlaneAvailable") != "True" && clusters.ConditionStatus(c, "Available") != "True" {
		return reconcile.Result{RequeueAfter: 30 * time.Second}, nil
	}
	kubeconfig := &corev1.Secret{}
	if err := r.Client.Get(ctx, types.NamespacedName{Namespace: req.Namespace, Name: req.Name + "-kubeconfig"}, kubeconfig); err != nil {
		if apierrors.IsNotFound(err) {
			return reconcile.Result{RequeueAfter: 30 * time.Second}, nil
		}
		return reconcile.Result{}, err
	}
	agent, err := r.Registry.Ensure(ctx, id)
	if err != nil {
		return reconcile.Result{}, err
	}
	objs, err := AgentManifest(r.Options, agent.RegistrationToken)
	if err != nil {
		return reconcile.Result{}, err
	}
	hash := manifestHash(objs)
	sec := &corev1.Secret{}
	if err := r.Client.Get(ctx, types.NamespacedName{Namespace: r.Registry.Namespace, Name: tunnel.SecretName(id)}, sec); err != nil {
		return reconcile.Result{}, err
	}
	if sec.Annotations[annotAgentManifest] == hash {
		return reconcile.Result{RequeueAfter: 10 * time.Minute}, nil
	}
	newClient := r.NewChildClient
	if newClient == nil {
		newClient = defaultChildClient
	}
	child, err := newClient(kubeconfig.Data["value"])
	if err != nil {
		return reconcile.Result{}, fmt.Errorf("the CAPI kubeconfig for %s: %w", id, err)
	}
	for _, o := range objs {
		if err := child.ApplyObject(ctx, o); err != nil {
			lg.Info("installing the agent failed; retrying", "error", err.Error())
			return reconcile.Result{RequeueAfter: 30 * time.Second}, nil
		}
	}
	patch := client.MergeFrom(sec.DeepCopy())
	if sec.Annotations == nil {
		sec.Annotations = map[string]string{}
	}
	sec.Annotations[annotAgentManifest] = hash
	if err := r.Client.Patch(ctx, sec, patch); err != nil {
		return reconcile.Result{}, err
	}
	lg.Info("installed cattle-cluster-agent", "image", r.Options.Image, "server", r.Options.HostURL)
	return reconcile.Result{RequeueAfter: 10 * time.Minute}, nil
}

func manifestHash(objs []*unstructured.Unstructured) string {
	h := sha256.New()
	for _, o := range objs {
		b, _ := o.MarshalJSON()
		h.Write(b)
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// CAChecksum is CATTLE_CA_CHECKSUM for a PEM CA: the sha256 of it with one
// trailing newline, as Rancher computes it. The agent checks it against
// /v3/settings/cacerts' value plus the newline jq prints after it.
func CAChecksum(pem string) string {
	pem = strings.TrimRight(pem, "\n")
	if pem == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(pem + "\n"))
	return hex.EncodeToString(sum[:])
}

// AgentManifest is what hyve installs on a child cluster: Rancher's own
// import manifest for cattle-cluster-agent (rancher/rancher
// pkg/systemtemplate), with the agent's multi-cluster-management feature
// off, so it only keeps the tunnel to hyve open; and the admin user's
// cluster-admin binding.
func AgentManifest(o AgentOptions, token string) ([]*unstructured.Unstructured, error) {
	image := o.Image
	if image == "" {
		image = DefaultAgentImage
	}
	tokenKey := sha256.Sum256([]byte(token))
	cred := "cattle-credentials-" + hex.EncodeToString(tokenKey[:])[:7]
	admin := o.AdminUser
	if admin == "" {
		admin = "admin"
	}
	manifest := fmt.Sprintf(`
apiVersion: v1
kind: Namespace
metadata:
  name: cattle-system
---
apiVersion: v1
kind: ServiceAccount
metadata: {name: cattle, namespace: cattle-system}
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: cattle-admin
  labels: {app.kubernetes.io/managed-by: hyve}
rules:
- apiGroups: ['*']
  resources: ['*']
  verbs: ['*']
- nonResourceURLs: ['*']
  verbs: ['*']
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: cattle-admin-binding
  labels: {app.kubernetes.io/managed-by: hyve}
subjects:
- {kind: ServiceAccount, name: cattle, namespace: cattle-system}
roleRef: {kind: ClusterRole, name: cattle-admin, apiGroup: rbac.authorization.k8s.io}
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: hyve-admin
  labels: {app.kubernetes.io/managed-by: hyve}
subjects:
- {kind: User, name: %q, apiGroup: rbac.authorization.k8s.io}
roleRef: {kind: ClusterRole, name: cluster-admin, apiGroup: rbac.authorization.k8s.io}
---
apiVersion: v1
kind: Secret
metadata: {name: %s, namespace: cattle-system}
type: Opaque
stringData:
  url: %q
  token: %q
  namespace: ""
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: cattle-cluster-agent
  namespace: cattle-system
  labels: {app.kubernetes.io/managed-by: hyve}
spec:
  selector:
    matchLabels: {app: cattle-cluster-agent}
  strategy:
    type: RollingUpdate
    rollingUpdate: {maxUnavailable: 0, maxSurge: 1}
  template:
    metadata:
      labels: {app: cattle-cluster-agent}
    spec:
      serviceAccountName: cattle
      tolerations:
      - {effect: NoSchedule, key: node-role.kubernetes.io/controlplane, value: "true"}
      - {effect: NoSchedule, key: node-role.kubernetes.io/control-plane, operator: Exists}
      containers:
      - name: cluster-register
        image: %q
        imagePullPolicy: IfNotPresent
        env:
        - {name: CATTLE_FEATURES, value: "multi-cluster-management-agent=false"}
        - {name: CATTLE_SERVER, value: %q}
        - {name: CATTLE_CA_CHECKSUM, value: %q}
        - {name: CATTLE_CLUSTER, value: "true"}
        - {name: CATTLE_K8S_MANAGED, value: "true"}
        - {name: CATTLE_CREDENTIAL_NAME, value: %s}
        resources:
          requests: {cpu: 10m, memory: 64Mi}
        volumeMounts:
        - {name: cattle-credentials, mountPath: /cattle-credentials, readOnly: true}
      volumes:
      - name: cattle-credentials
        secret: {secretName: %s, defaultMode: 320}
`, "hyve:"+admin, cred, o.HostURL, token, image, o.HostURL, CAChecksum(o.CACerts), cred, cred)
	var out []*unstructured.Unstructured
	for _, doc := range strings.Split(manifest, "\n---\n") {
		if strings.TrimSpace(doc) == "" {
			continue
		}
		m := map[string]any{}
		if err := yaml.Unmarshal([]byte(doc), &m); err != nil {
			return nil, err
		}
		out = append(out, &unstructured.Unstructured{Object: m})
	}
	return out, nil
}
