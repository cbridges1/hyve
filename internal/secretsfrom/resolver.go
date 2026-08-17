// Package secretsfrom resolves a workflow's or module operation's
// spec.secretsFrom entries — declared references to a Kubernetes Secret on
// some already-managed cluster — into a plain map of env-var-name -> value.
//
// It has no dependency on internal/workflow or internal/module, by design:
// both of those packages need to resolve secretsFrom (the former for
// runtime: client workflows, the latter for module operations — see
// HYVE-IMPLEMENTATION-PLAN.md's Phase 5), and internal/module deliberately
// never imports internal/workflow (module/executor.go's own doc comment
// explains why: it keeps the module system decoupled from the workflow
// executor). Living in its own leaf package lets both sides share the same
// type and resolution logic without creating that cycle or duplicating it.
package secretsfrom

import (
	"context"
	"fmt"
	"os"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

// SecretKeyMap maps one key in the source Secret to the env var it should
// populate. Env defaults to Key when left empty.
type SecretKeyMap struct {
	Key string `yaml:"key" json:"key"`
	Env string `yaml:"env,omitempty" json:"env,omitempty"`
}

// SecretSource declares one Kubernetes Secret to resolve into env vars
// before a workflow's (or module operation's) steps run. Cluster names an
// already-managed cluster — resolution reaches it via whatever kubeconfig
// the caller already has for it (see KubeconfigLocator), not a new access
// path, so a caller not authorized to read the Secret gets back the same
// RBAC error `kubectl get secret` would give them today.
type SecretSource struct {
	Cluster   string         `yaml:"cluster" json:"cluster"`
	Namespace string         `yaml:"namespace" json:"namespace"`
	SecretRef string         `yaml:"secretRef" json:"secretRef"`
	Keys      []SecretKeyMap `yaml:"keys" json:"keys"`
}

// KubeconfigLocator resolves a cluster name to the path of a kubeconfig
// file that can reach it. Dependency-injected rather than imported directly
// (the real implementation every caller passes is
// module.KubeconfigPathForCluster) so this package stays free of any
// import on internal/module.
type KubeconfigLocator func(clusterName string) (string, error)

// Resolve fetches src's Secret via a client built from the kubeconfig
// locate(src.Cluster) returns, and extracts src.Keys into a map of
// env-var-name -> value.
func Resolve(ctx context.Context, locate KubeconfigLocator, src SecretSource) (map[string]string, error) {
	if err := validate(src); err != nil {
		return nil, err
	}

	kcPath, err := locate(src.Cluster)
	if err != nil {
		return nil, fmt.Errorf("secretsFrom: resolve kubeconfig for cluster %q: %w", src.Cluster, err)
	}
	if _, statErr := os.Stat(kcPath); statErr != nil {
		return nil, fmt.Errorf("secretsFrom: cluster %q has no kubeconfig yet at %s — has it been reconciled (auth run) at least once?", src.Cluster, kcPath)
	}

	config, err := clientcmd.BuildConfigFromFlags("", kcPath)
	if err != nil {
		return nil, fmt.Errorf("secretsFrom: build client config for cluster %q: %w", src.Cluster, err)
	}
	client, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("secretsFrom: build client for cluster %q: %w", src.Cluster, err)
	}

	return resolveWithClient(ctx, client, src)
}

// resolveWithClient does the actual Kubernetes-facing work, separated from
// Resolve so unit tests can inject client-go's fake clientset instead of
// requiring a real kubeconfig file — the same split KubernetesJobStepRunner
// uses (Client kubernetes.Interface) for the same reason.
func resolveWithClient(ctx context.Context, client kubernetes.Interface, src SecretSource) (map[string]string, error) {
	secret, err := client.CoreV1().Secrets(src.Namespace).Get(ctx, src.SecretRef, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("secretsFrom: get secret %s/%s on cluster %q: %w", src.Namespace, src.SecretRef, src.Cluster, err)
	}

	resolved := make(map[string]string, len(src.Keys))
	var missing []string
	for _, k := range src.Keys {
		val, ok := secretValue(secret, k.Key)
		if !ok {
			missing = append(missing, k.Key)
			continue
		}
		env := k.Env
		if env == "" {
			env = k.Key
		}
		resolved[env] = val
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("secretsFrom: secret %s/%s on cluster %q missing key(s): %s", src.Namespace, src.SecretRef, src.Cluster, strings.Join(missing, ", "))
	}
	return resolved, nil
}

// secretValue reads key from secret.Data first (the common case — raw
// bytes, already base64-decoded by client-go) and falls back to
// secret.StringData (only ever populated on a Secret this same client just
// wrote and hasn't re-fetched — client-go's fake clientset doesn't move
// StringData into Data the way a real API server does on write, so tests
// constructing a fake Secret literal with StringData still resolve
// correctly here).
func secretValue(secret *corev1.Secret, key string) (string, bool) {
	if val, ok := secret.Data[key]; ok {
		return string(val), true
	}
	if val, ok := secret.StringData[key]; ok {
		return val, true
	}
	return "", false
}

// validate checks the required fields on src are present — pure,
// table-testable.
func validate(src SecretSource) error {
	if src.Cluster == "" {
		return fmt.Errorf("secretsFrom: cluster is required")
	}
	if src.Namespace == "" {
		return fmt.Errorf("secretsFrom: namespace is required")
	}
	if src.SecretRef == "" {
		return fmt.Errorf("secretsFrom: secretRef is required")
	}
	if len(src.Keys) == 0 {
		return fmt.Errorf("secretsFrom: at least one entry in keys is required")
	}
	return nil
}
