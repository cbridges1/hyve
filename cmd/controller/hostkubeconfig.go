package controller

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/cbridges1/hyve/internal/hostauth"

	"k8s.io/client-go/kubernetes"
)

// hostKubeconfigIssuer implements reconcile.HostKubeconfigIssuer — mints a
// kubeconfig for hyve's own host cluster, pointed directly at
// https://kubernetes.default.svc (no /proxy hop needed: hyve-controller
// already runs inside the target cluster, unlike an external kubectl
// caller — see internal/api's HostProvider for that other case, which
// shares the same underlying internal/hostauth.MintKubeconfig).
type hostKubeconfigIssuer struct {
	Clientset              kubernetes.Interface
	Namespace              string // where the HostServiceAccount lives — this controller's own --namespace
	HostServiceAccountName string
	CAPath                 string // this pod's own in-cluster CA
}

func (h *hostKubeconfigIssuer) MintHostKubeconfig(ctx context.Context) ([]byte, error) {
	caData, err := os.ReadFile(h.CAPath)
	if err != nil {
		return nil, fmt.Errorf("read in-cluster CA at %s: %w", h.CAPath, err)
	}
	return hostauth.MintKubeconfig(ctx, h.Clientset, h.Namespace, h.HostServiceAccountName, "https://kubernetes.default.svc", caData, time.Hour)
}
