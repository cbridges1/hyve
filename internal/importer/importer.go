// Package importer brings an existing cluster under hyve without a
// provider ('hyve cluster import'): a ServiceAccount on the cluster gives
// hyve a kubeconfig of its own, and a Cluster API Cluster whose
// infrastructure and control plane are hyve's ImportedCluster and
// ImportedControlPlane points at it. hyve-controller reports those ready
// while the cluster answers, so Cluster API, CAAPF (Fleet) and hyve's agent
// treat it like any other cluster. hyve never creates, scales or deletes an
// imported cluster; deleting its Cluster detaches it (Detach).
package importer

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"time"

	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"

	"github.com/cbridges1/hyve/internal/clusters"
	"github.com/cbridges1/hyve/internal/kube"
)

// The objects hyve makes on an imported cluster for its own access. The
// ServiceAccount and its token Secret are owned by the binding, so deleting
// the binding (the last thing Detach does) removes all three.
const (
	AccessNamespace = "kube-system"
	AccessName      = "hyve-import"
	tokenSecret     = "hyve-import-token"
)

// ClusterNameLabel is Cluster API's label naming an object's Cluster.
const ClusterNameLabel = "cluster.x-k8s.io/cluster-name"

// Access is what Prepare made: a kubeconfig for the cluster's hyve-import
// ServiceAccount and the Kubernetes version it reports.
type Access struct {
	Kubeconfig []byte
	Version    string
}

// Prepare gives hyve cluster-admin on the cluster cfg reaches (as Rancher's
// import does), with a ServiceAccount token, and returns a kubeconfig that
// uses it. server is the API server's address as hyve's host reaches it;
// empty uses cfg's. Running it again reuses the ServiceAccount.
func Prepare(ctx context.Context, cfg *rest.Config, server string) (*Access, error) {
	cfg = rest.CopyConfig(cfg)
	cfg.Timeout = 30 * time.Second
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, err
	}
	v, err := cs.Discovery().ServerVersion()
	if err != nil {
		return nil, fmt.Errorf("can't reach the cluster at %s: %w", cfg.Host, err)
	}
	labels := map[string]string{clusters.LabelManagedBy: clusters.ManagedBy}
	crb, err := cs.RbacV1().ClusterRoleBindings().Get(ctx, AccessName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		crb, err = cs.RbacV1().ClusterRoleBindings().Create(ctx, &rbacv1.ClusterRoleBinding{
			ObjectMeta: metav1.ObjectMeta{Name: AccessName, Labels: labels},
			RoleRef:    rbacv1.RoleRef{APIGroup: "rbac.authorization.k8s.io", Kind: "ClusterRole", Name: "cluster-admin"},
			Subjects:   []rbacv1.Subject{{Kind: "ServiceAccount", Name: AccessName, Namespace: AccessNamespace}},
		}, metav1.CreateOptions{})
	}
	if err != nil {
		return nil, fmt.Errorf("ClusterRoleBinding %s: %w", AccessName, err)
	}
	owner := []metav1.OwnerReference{{APIVersion: "rbac.authorization.k8s.io/v1", Kind: "ClusterRoleBinding", Name: crb.Name, UID: crb.UID}}
	_, err = cs.CoreV1().ServiceAccounts(AccessNamespace).Create(ctx, &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{Name: AccessName, Namespace: AccessNamespace, Labels: labels, OwnerReferences: owner},
	}, metav1.CreateOptions{})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		return nil, fmt.Errorf("ServiceAccount %s/%s: %w", AccessNamespace, AccessName, err)
	}
	_, err = cs.CoreV1().Secrets(AccessNamespace).Create(ctx, &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: tokenSecret, Namespace: AccessNamespace, Labels: labels, OwnerReferences: owner,
			Annotations: map[string]string{corev1.ServiceAccountNameKey: AccessName}},
		Type: corev1.SecretTypeServiceAccountToken,
	}, metav1.CreateOptions{})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		return nil, fmt.Errorf("Secret %s/%s: %w", AccessNamespace, tokenSecret, err)
	}
	var token []byte
	err = kube.Poll(ctx, time.Second, 30*time.Second, nil, func(ctx context.Context) (bool, string, error) {
		s, err := cs.CoreV1().Secrets(AccessNamespace).Get(ctx, tokenSecret, metav1.GetOptions{})
		if err != nil {
			return false, "", err
		}
		token = s.Data[corev1.ServiceAccountTokenKey]
		return len(token) > 0, "", nil
	})
	if err != nil {
		return nil, fmt.Errorf("waiting for the hyve-import token: %w", err)
	}
	if server == "" {
		server = cfg.Host
	}
	ca := cfg.CAData
	if len(ca) == 0 && cfg.CAFile != "" {
		if ca, err = os.ReadFile(cfg.CAFile); err != nil {
			return nil, err
		}
	}
	kc := clientcmdapi.NewConfig()
	kc.Clusters["cluster"] = &clientcmdapi.Cluster{Server: server, CertificateAuthorityData: ca, InsecureSkipTLSVerify: cfg.Insecure && len(ca) == 0}
	kc.AuthInfos["hyve-import"] = &clientcmdapi.AuthInfo{Token: string(token)}
	kc.Contexts["cluster"] = &clientcmdapi.Context{Cluster: "cluster", AuthInfo: "hyve-import"}
	kc.CurrentContext = "cluster"
	out, err := clientcmd.Write(*kc)
	if err != nil {
		return nil, err
	}
	return &Access{Kubeconfig: out, Version: v.GitVersion}, nil
}

// Create makes the host's objects for an imported cluster ns/name: the
// Cluster, its <name>-kubeconfig Secret (owned by the Cluster), and its
// ImportedCluster and ImportedControlPlane. c is the Cluster from
// clusters.NewImported, with any labels and annotations set.
func Create(ctx context.Context, dc dynamic.Interface, c *unstructured.Unstructured, kubeconfig []byte) error {
	ns, name := c.GetNamespace(), c.GetName()
	created, err := dc.Resource(clusters.ClusterGVR).Namespace(ns).Create(ctx, c, metav1.CreateOptions{FieldManager: kube.FieldManager})
	if err != nil {
		return err
	}
	lbl := map[string]any{ClusterNameLabel: name, clusters.LabelManagedBy: clusters.ManagedBy}
	sec := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1", "kind": "Secret", "type": "cluster.x-k8s.io/secret",
		"metadata": map[string]any{"name": name + "-kubeconfig", "namespace": ns, "labels": lbl,
			"ownerReferences": []any{map[string]any{"apiVersion": "cluster.x-k8s.io/v1beta2", "kind": "Cluster", "name": name, "uid": string(created.GetUID())}}},
		"stringData": map[string]any{"value": string(kubeconfig)},
	}}
	secrets := dc.Resource(corev1.SchemeGroupVersion.WithResource("secrets")).Namespace(ns)
	if _, err := secrets.Create(ctx, sec, metav1.CreateOptions{FieldManager: kube.FieldManager}); err != nil {
		return fmt.Errorf("Secret %s-kubeconfig: %w", name, err)
	}
	for _, gvr := range []struct {
		r    dynamic.NamespaceableResourceInterface
		kind string
	}{
		{dc.Resource(clusters.ImportedClusterGVR), clusters.ImportedClusterKind},
		{dc.Resource(clusters.ImportedControlPlaneGVR), clusters.ImportedControlPlaneKind},
	} {
		o := &unstructured.Unstructured{Object: map[string]any{
			"apiVersion": clusters.ImportedGroup + "/v1alpha1", "kind": gvr.kind,
			"metadata": map[string]any{"name": name, "namespace": ns, "labels": lbl},
			"spec":     map[string]any{},
		}}
		if _, err := gvr.r.Namespace(ns).Create(ctx, o, metav1.CreateOptions{FieldManager: kube.FieldManager}); err != nil {
			return fmt.Errorf("%s %s: %w", gvr.kind, name, err)
		}
	}
	return nil
}

// Endpoint is the host and port of a kubeconfig's server.
func Endpoint(server string) (string, int64, error) {
	u, err := url.Parse(server)
	if err != nil || u.Host == "" {
		return "", 0, fmt.Errorf("server %q isn't a URL", server)
	}
	host, port := u.Hostname(), int64(443)
	if p := u.Port(); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil {
			return "", 0, fmt.Errorf("server %q: %w", server, err)
		}
		port = int64(n)
	}
	return host, port, nil
}

// Agents are what hyve and Fleet installed on a cluster: Detach removes
// them.
var agents = []struct{ namespace, name string }{
	{"cattle-system", "cattle-cluster-agent"},
	{"cattle-fleet-system", "fleet-agent"},
}

// Detach removes from an imported cluster what hyve put there: the agents
// (hyve's cattle-cluster-agent and Fleet's fleet-agent), the ClusterRoles
// and bindings labelled as hyve's, and last its own access (the
// hyve-import binding, which takes the ServiceAccount and token with it).
// What Fleet deployed stays. It's best effort: an unreachable cluster keeps
// them.
func Detach(ctx context.Context, kubeconfig []byte) error {
	cfg, err := clientcmd.RESTConfigFromKubeConfig(kubeconfig)
	if err != nil {
		return err
	}
	cfg.Timeout = 15 * time.Second
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return err
	}
	var errs []error
	ignore := func(err error) error {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return err
	}
	for _, a := range agents {
		errs = append(errs, ignore(cs.AppsV1().Deployments(a.namespace).Delete(ctx, a.name, metav1.DeleteOptions{})))
	}
	sel := metav1.ListOptions{LabelSelector: clusters.LabelManagedBy + "=" + clusters.ManagedBy}
	if crbs, err := cs.RbacV1().ClusterRoleBindings().List(ctx, sel); err == nil {
		for _, b := range crbs.Items {
			if b.Name != AccessName {
				errs = append(errs, ignore(cs.RbacV1().ClusterRoleBindings().Delete(ctx, b.Name, metav1.DeleteOptions{})))
			}
		}
	} else {
		errs = append(errs, err)
	}
	errs = append(errs, ignore(cs.RbacV1().ClusterRoles().DeleteCollection(ctx, metav1.DeleteOptions{}, sel)))
	// Last: after this, hyve's token no longer works.
	bg := metav1.DeletePropagationBackground
	errs = append(errs, ignore(cs.RbacV1().ClusterRoleBindings().Delete(ctx, AccessName, metav1.DeleteOptions{PropagationPolicy: &bg})))
	return errors.Join(errs...)
}
