package operator

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/cbridges1/hyve/internal/clusters"
	"github.com/cbridges1/hyve/internal/importer"
	"github.com/cbridges1/hyve/internal/templates"
)

// importedFinalizer keeps an ImportedControlPlane until what hyve put on
// its cluster is removed (importer.Detach).
const importedFinalizer = "hyve.io/detach"

var (
	bundleNamespaceMappingGVK = schema.GroupVersionKind{Group: "fleet.cattle.io", Version: "v1alpha1", Kind: "BundleNamespaceMapping"}
	importedClusterGVK        = clusters.ImportedClusterGVR.GroupVersion().WithKind(clusters.ImportedClusterKind)
	importedControlPlaneGVK   = clusters.ImportedControlPlaneGVR.GroupVersion().WithKind(clusters.ImportedControlPlaneKind)
)

func setupImported(mgr ctrl.Manager) error {
	if err := ctrl.NewControllerManagedBy(mgr).Named("hyve-imported-cluster").For(object(importedClusterGVK)).
		Complete(&ImportedCluster{Client: mgr.GetClient(), Now: time.Now}); err != nil {
		return err
	}
	return ctrl.NewControllerManagedBy(mgr).Named("hyve-imported-control-plane").For(object(importedControlPlaneGVK)).
		Complete(&ImportedControlPlane{Client: mgr.GetClient(), Now: time.Now})
}

// ImportedCluster is an imported cluster's infrastructure: there's none to
// provision, so it's reported provisioned.
type ImportedCluster struct {
	Client client.Client
	Now    func() time.Time
}

// Reconcile implements reconcile.Reconciler.
func (r *ImportedCluster) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	o := object(importedClusterGVK)
	if err := r.Client.Get(ctx, req.NamespacedName, o); err != nil {
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}
	if o.GetDeletionTimestamp() != nil {
		return reconcile.Result{}, nil
	}
	base := o.DeepCopy()
	ensureStatus(o)
	_ = unstructured.SetNestedField(o.Object, true, "status", "initialization", "provisioned")
	setCondition(o, "Ready", true, "Imported", "", r.Now())
	return reconcile.Result{}, r.Client.Status().Patch(ctx, o, client.MergeFrom(base))
}

// ImportedControlPlane is an imported cluster's control plane: available
// while the cluster answers through its <cluster>-kubeconfig Secret, with
// the endpoint and version it reports. Deleting it detaches the cluster.
type ImportedControlPlane struct {
	Client client.Client
	Now    func() time.Time
	// Probe returns the version of the cluster a kubeconfig reaches, and
	// Detach cleans it up; replaced in tests.
	Probe  func(ctx context.Context, kubeconfig []byte) (string, error)
	Detach func(ctx context.Context, kubeconfig []byte) error
}

// Reconcile implements reconcile.Reconciler.
func (r *ImportedControlPlane) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	lg := log.FromContext(ctx).WithValues("importedcontrolplane", req.NamespacedName)
	cp := object(importedControlPlaneGVK)
	if err := r.Client.Get(ctx, req.NamespacedName, cp); err != nil {
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}
	name := cp.GetLabels()[importer.ClusterNameLabel]
	if name == "" {
		name = cp.GetName()
	}
	kubeconfig, err := r.kubeconfig(ctx, req.Namespace, name)
	if err != nil {
		return reconcile.Result{}, err
	}

	if cp.GetDeletionTimestamp() != nil {
		if !controllerutil.ContainsFinalizer(cp, importedFinalizer) {
			return reconcile.Result{}, nil
		}
		if kubeconfig != nil {
			detach := r.Detach
			if detach == nil {
				detach = importer.Detach
			}
			// Once only: an unreachable cluster keeps what hyve put there.
			if err := detach(ctx, kubeconfig); err != nil {
				lg.Info("couldn't remove everything hyve installed on the cluster", "error", err.Error())
			} else {
				lg.Info("detached the cluster: removed hyve's agents and access")
			}
		}
		controllerutil.RemoveFinalizer(cp, importedFinalizer)
		return reconcile.Result{}, r.Client.Update(ctx, cp)
	}
	if controllerutil.AddFinalizer(cp, importedFinalizer) {
		if err := r.Client.Update(ctx, cp); err != nil {
			return reconcile.Result{}, err
		}
	}

	if err := r.ensureMapping(ctx, req.Namespace, name); err != nil {
		return reconcile.Result{}, err
	}

	base := cp.DeepCopy()
	ensureStatus(cp)
	if kubeconfig == nil {
		setCondition(cp, "Available", false, "WaitingForKubeconfig", "Secret "+name+"-kubeconfig not found", r.Now())
		return reconcile.Result{RequeueAfter: 30 * time.Second}, r.Client.Status().Patch(ctx, cp, client.MergeFrom(base))
	}
	cfg, err := clientcmd.Load(kubeconfig)
	if err != nil {
		return reconcile.Result{}, fmt.Errorf("Secret %s-kubeconfig: %w", name, err)
	}
	server := ""
	if ctxt := cfg.Contexts[cfg.CurrentContext]; ctxt != nil && cfg.Clusters[ctxt.Cluster] != nil {
		server = cfg.Clusters[ctxt.Cluster].Server
	}
	host, port, err := importer.Endpoint(server)
	if err != nil {
		return reconcile.Result{}, fmt.Errorf("Secret %s-kubeconfig: %w", name, err)
	}
	h, _, _ := unstructured.NestedString(cp.Object, "spec", "controlPlaneEndpoint", "host")
	p, _, _ := unstructured.NestedInt64(cp.Object, "spec", "controlPlaneEndpoint", "port")
	if h != host || p != port {
		_ = unstructured.SetNestedField(cp.Object, map[string]any{"host": host, "port": port}, "spec", "controlPlaneEndpoint")
		if err := r.Client.Patch(ctx, cp, client.MergeFrom(base)); err != nil {
			return reconcile.Result{}, err
		}
		base = cp.DeepCopy()
	}

	ensureStatus(cp)
	probe := r.Probe
	if probe == nil {
		probe = probeVersion
	}
	version, err := probe(ctx, kubeconfig)
	if err != nil {
		setCondition(cp, "Available", false, "Unreachable", err.Error(), r.Now())
	} else {
		// Initialized once reached, and stays so: Cluster API (and CAAPF)
		// don't un-initialize a control plane.
		_ = unstructured.SetNestedField(cp.Object, true, "status", "initialization", "controlPlaneInitialized")
		_ = unstructured.SetNestedField(cp.Object, version, "status", "version")
		setCondition(cp, "Available", true, "Reachable", "Kubernetes "+version, r.Now())
	}
	if err := r.Client.Status().Patch(ctx, cp, client.MergeFrom(base)); err != nil {
		return reconcile.Result{}, err
	}
	return reconcile.Result{RequeueAfter: 2 * time.Minute}, nil
}

// ensureMapping lets the bundles of an imported cluster's template, in
// another namespace, reach it: the BundleNamespaceMapping CAAPF makes for
// a Cluster whose ClusterClass is in another namespace, which an imported
// cluster hasn't got. It has the same name and content as CAAPF's, so the
// two agree; like CAAPF's, it stays when the cluster goes.
func (r *ImportedControlPlane) ensureMapping(ctx context.Context, ns, name string) error {
	c := object(clusterGVK)
	if err := r.Client.Get(ctx, types.NamespacedName{Namespace: ns, Name: name}, c); err != nil {
		return client.IgnoreNotFound(err)
	}
	tn, tns := templates.Ref(c)
	if tn == "" || tns == ns {
		return nil
	}
	m := object(bundleNamespaceMappingGVK)
	err := r.Client.Get(ctx, types.NamespacedName{Namespace: tns, Name: ns}, m)
	if err == nil || !apierrors.IsNotFound(err) {
		return err
	}
	m.SetNamespace(tns)
	m.SetName(ns)
	m.Object["bundleSelector"] = map[string]any{}
	m.Object["namespaceSelector"] = map[string]any{"matchLabels": map[string]any{"kubernetes.io/metadata.name": ns}}
	if err := r.Client.Create(ctx, m); err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("BundleNamespaceMapping %s/%s: %w", tns, ns, err)
	}
	// Fleet doesn't retarget bundles when a mapping appears; a change to
	// one makes it.
	return r.retarget(ctx, tns, tn)
}

// retarget touches the bundles in ns aimed at template tn's clusters, so
// Fleet works out their targets again.
func (r *ImportedControlPlane) retarget(ctx context.Context, ns, tn string) error {
	list := &unstructured.UnstructuredList{}
	list.SetGroupVersionKind(bundleGVK.GroupVersion().WithKind("BundleList"))
	if err := r.Client.List(ctx, list, client.InNamespace(ns)); err != nil {
		return err
	}
	for i := range list.Items {
		b := &list.Items[i]
		targets, _, _ := unstructured.NestedSlice(b.Object, "spec", "targets")
		aimed := false
		for _, t := range targets {
			if v, _, _ := unstructured.NestedString(t.(map[string]any), "clusterSelector", "matchLabels", templates.LabelTemplate); v == tn {
				aimed = true
			}
		}
		if !aimed {
			continue
		}
		patch := client.MergeFrom(b.DeepCopy())
		ann := b.GetAnnotations()
		if ann == nil {
			ann = map[string]string{}
		}
		ann["hyve.io/retargeted"] = r.Now().UTC().Format(time.RFC3339)
		b.SetAnnotations(ann)
		if err := r.Client.Patch(ctx, b, patch); err != nil && !apierrors.IsNotFound(err) {
			return err
		}
	}
	return nil
}

// kubeconfig returns a cluster's <name>-kubeconfig Secret's value, or nil
// when there's none.
func (r *ImportedControlPlane) kubeconfig(ctx context.Context, ns, name string) ([]byte, error) {
	s := &corev1.Secret{}
	err := r.Client.Get(ctx, types.NamespacedName{Namespace: ns, Name: name + "-kubeconfig"}, s)
	if apierrors.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return s.Data["value"], nil
}

func probeVersion(_ context.Context, kubeconfig []byte) (string, error) {
	cfg, err := clientcmd.RESTConfigFromKubeConfig(kubeconfig)
	if err != nil {
		return "", err
	}
	cfg.Timeout = 15 * time.Second
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return "", err
	}
	v, err := cs.Discovery().ServerVersion()
	if err != nil {
		return "", err
	}
	return v.GitVersion, nil
}

// ensureStatus makes o's status a map, so fields can be set in it (a
// read can return it as null).
func ensureStatus(o *unstructured.Unstructured) {
	if m, _ := o.Object["status"].(map[string]any); m == nil {
		o.Object["status"] = map[string]any{}
	}
}

// setCondition sets a metav1.Condition in o's status.conditions, keeping
// its transition time while its status doesn't change.
func setCondition(o *unstructured.Unstructured, typ string, ok bool, reason, message string, now time.Time) {
	status := "False"
	if ok {
		status = "True"
	}
	conds, _, _ := unstructured.NestedSlice(o.Object, "status", "conditions")
	c := map[string]any{"type": typ, "status": status, "reason": reason, "message": message,
		"lastTransitionTime": now.UTC().Format(time.RFC3339), "observedGeneration": o.GetGeneration()}
	for i, x := range conds {
		if m, _ := x.(map[string]any); m != nil && m["type"] == typ {
			if m["status"] == status {
				c["lastTransitionTime"] = m["lastTransitionTime"]
			}
			conds[i] = c
			_ = unstructured.SetNestedSlice(o.Object, conds, "status", "conditions")
			return
		}
	}
	_ = unstructured.SetNestedSlice(o.Object, append(conds, c), "status", "conditions")
}
