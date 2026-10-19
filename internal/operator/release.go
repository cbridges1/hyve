package operator

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/cbridges1/hyve/internal/clusters"
	"github.com/cbridges1/hyve/internal/kube"
	"github.com/cbridges1/hyve/internal/tunnel"
)

// ReleaseAgents hands a cluster's agent to Rancher after Rancher was added
// to a standalone install (open question 3). Turtles imports the cluster by
// applying Rancher's import manifest, but leaves a cattle-cluster-agent
// Deployment that's already there alone, so the agent hyve installed keeps
// dialing hyve's host, which is gone. hyve deletes the Deployment it
// applied, and Turtles' next import attempt creates Rancher's.
type ReleaseAgents struct {
	Client client.Client
	// Namespace holds the host's per-cluster agent Secrets: a cluster with
	// one had its agent installed by hyve.
	Namespace string
	// NewChild builds a client for a child from its kubeconfig; replaced
	// in tests.
	NewChild func(kubeconfig []byte) (kubernetes.Interface, error)
}

func defaultChildClientset(kubeconfig []byte) (kubernetes.Interface, error) {
	cfg, err := clientcmd.RESTConfigFromKubeConfig(kubeconfig)
	if err != nil {
		return nil, err
	}
	cfg.Timeout = 30 * time.Second
	return kubernetes.NewForConfig(cfg)
}

// Reconcile implements reconcile.Reconciler.
func (r *ReleaseAgents) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	id := tunnel.ClusterID(req.Namespace, req.Name)
	reg := &corev1.Secret{}
	if err := r.Client.Get(ctx, types.NamespacedName{Namespace: r.Namespace, Name: tunnel.SecretName(id)}, reg); err != nil {
		return reconcile.Result{}, client.IgnoreNotFound(err) // hyve never installed an agent here
	}
	c := object(clusterGVK)
	if err := r.Client.Get(ctx, req.NamespacedName, c); err != nil {
		if apierrors.IsNotFound(err) {
			return reconcile.Result{}, client.IgnoreNotFound(r.Client.Delete(ctx, reg))
		}
		return reconcile.Result{}, err
	}
	if c.GetDeletionTimestamp() != nil || clusters.ConditionStatus(c, "ControlPlaneAvailable") != "True" {
		return reconcile.Result{RequeueAfter: time.Minute}, nil
	}
	kubeconfig := &corev1.Secret{}
	if err := r.Client.Get(ctx, types.NamespacedName{Namespace: req.Namespace, Name: req.Name + "-kubeconfig"}, kubeconfig); err != nil {
		return reconcile.Result{RequeueAfter: time.Minute}, client.IgnoreNotFound(err)
	}
	newChild := r.NewChild
	if newChild == nil {
		newChild = defaultChildClientset
	}
	child, err := newChild(kubeconfig.Data["value"])
	if err != nil {
		return reconcile.Result{}, fmt.Errorf("the CAPI kubeconfig for %s: %w", id, err)
	}
	deps := child.AppsV1().Deployments("cattle-system")
	d, err := deps.Get(ctx, "cattle-cluster-agent", metav1.GetOptions{})
	switch {
	case apierrors.IsNotFound(err):
	case err != nil:
		return reconcile.Result{RequeueAfter: time.Minute}, nil // not reachable yet
	case appliedBy(d.ManagedFields, kube.FieldManager):
		if err := deps.Delete(ctx, d.Name, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
			return reconcile.Result{RequeueAfter: time.Minute}, nil
		}
		log.FromContext(ctx).Info("removed hyve's cattle-cluster-agent; Rancher's import installs its own", "cluster", id)
	}
	// Done with this cluster: its agent is Rancher's from now on.
	return reconcile.Result{}, client.IgnoreNotFound(r.Client.Delete(ctx, reg))
}

func appliedBy(fields []metav1.ManagedFieldsEntry, manager string) bool {
	for _, f := range fields {
		if f.Manager == manager && f.Operation == metav1.ManagedFieldsOperationApply {
			return true
		}
	}
	return false
}
