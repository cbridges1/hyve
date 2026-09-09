package agentpki

import (
	"context"

	"k8s.io/client-go/kubernetes"
)

// TokenIssuer adapts GenerateBootstrapToken into internal/reconcile's own
// AgentTokenIssuer interface (internal/reconcile/agent.go) — that package
// stays decoupled from client-go and agentpki (mirrors how internal/workflow
// and internal/module's own client-go-dependent implementations,
// KubernetesJobStepRunner/JobRunner, live in their own packages, referenced
// only via an interface reconcile.go declares itself), so the one place a
// concrete kubernetes.Interface actually gets threaded in is here, wired up
// by cmd/controller/run.go alongside the same clientset it already builds
// for KubernetesJobStepRunner/JobRunner.
type TokenIssuer struct {
	Clientset kubernetes.Interface

	// ControlPlaneNamespace is where the bootstrap-token Secret itself is
	// stored (see GenerateBootstrapToken) — must match whatever namespace
	// hyve-api's own POST /agent/bootstrap handler reads it back from
	// (its own --namespace, cmd/api/run.go). Under today's one-namespace-
	// per-install model this is always the same value as hyve-controller's
	// own --namespace, the same value TargetNamespace below happens to
	// receive too — see internal/reconcile.Reconciler.AgentControlPlaneNamespace's
	// own doc comment for why these are kept as two separate call-site
	// values rather than one, despite currently always being equal.
	ControlPlaneNamespace string
}

// IssueBootstrapToken implements internal/reconcile's AgentTokenIssuer.
func (t *TokenIssuer) IssueBootstrapToken(ctx context.Context, targetNamespace, clusterName string) (string, error) {
	return GenerateBootstrapToken(ctx, t.Clientset, t.ControlPlaneNamespace, targetNamespace, clusterName)
}
