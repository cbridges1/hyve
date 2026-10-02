// Package hostauth mints a kubeconfig for hyve's own host cluster — the
// cluster hyve-controller/hyve-api themselves run on, marked on its own
// ClusterDefinition via access.method: primary (see
// hyvev1alpha1.AccessMethodPrimary's own doc comment). This is
// deliberately no-module: an earlier design required an admin to hand-write
// a driver module's auth.yaml just to get a kubeconfig for the cluster
// hyve is already running on, which added friction for no benefit — see
// docs/HYVE-AGENT-MIGRATION-GUIDE.md's "Host cluster access" section for
// the full history. Shared by internal/api (HostProvider, serving GET
// /api/kubeconfig for a caller's `hyve cluster auth`) and
// cmd/controller/run.go's own host-cluster spec.resources reconciliation
// path — the only two callers that need a credential for the host
// cluster, both minting against the same dedicated ServiceAccount (e.g.
// hyve-host-admin, bound to the built-in cluster-admin ClusterRole — see
// deploy/helm/hyve/templates/api-access-roles.yaml), just pointing the
// resulting kubeconfig's server: at two different addresses reachable
// from two different places (hyve-api's own /proxy for an external
// caller; https://kubernetes.default.svc directly for hyve-controller,
// which already runs inside the target cluster and needs no reverse-proxy
// hop).
package hostauth

import (
	"context"
	"fmt"
	"time"

	authenticationv1 "k8s.io/api/authentication/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

// MintKubeconfig mints a short-lived token for the ServiceAccount named
// saNamespace/saName via TokenRequest and packages it into a minimal,
// valid kubeconfig whose server: is server, trusting caData for that
// connection.
func MintKubeconfig(ctx context.Context, clientset kubernetes.Interface, saNamespace, saName, server string, caData []byte, ttl time.Duration) ([]byte, error) {
	expSeconds := int64(ttl.Seconds())
	tr, err := clientset.CoreV1().ServiceAccounts(saNamespace).CreateToken(ctx, saName, &authenticationv1.TokenRequest{
		Spec: authenticationv1.TokenRequestSpec{ExpirationSeconds: &expSeconds},
	}, metav1.CreateOptions{})
	if err != nil {
		return nil, fmt.Errorf("mint token for %s/%s: %w", saNamespace, saName, err)
	}
	return buildKubeconfig(server, caData, tr.Status.Token)
}

// buildKubeconfig assembles a minimal, valid kubeconfig YAML via
// client-go's own clientcmd types rather than hand-formatting YAML —
// avoids subtle serialization bugs (quoting, base64 wrapping) a
// string-templated kubeconfig would risk.
func buildKubeconfig(server string, caData []byte, token string) ([]byte, error) {
	cfg := clientcmdapi.NewConfig()
	cfg.Clusters["hyve"] = &clientcmdapi.Cluster{Server: server, CertificateAuthorityData: caData}
	cfg.AuthInfos["hyve"] = &clientcmdapi.AuthInfo{Token: token}
	cfg.Contexts["hyve"] = &clientcmdapi.Context{Cluster: "hyve", AuthInfo: "hyve"}
	cfg.CurrentContext = "hyve"
	return clientcmd.Write(*cfg)
}
