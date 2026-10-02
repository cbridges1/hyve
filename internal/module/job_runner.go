package module

import (
	"context"
	"time"

	"k8s.io/client-go/kubernetes"

	"github.com/cbridges1/hyve/internal/k8sjob"
)

// JobRunner dispatches module create/status/delete/auth execution to a
// short-lived batch/v1.Job — cluster mode's counterpart to
// workflow.KubernetesJobStepRunner, sharing the same internal/k8sjob
// lifecycle. Executor.Runner is nil in local/CLI mode (today's inline
// os/exec path, unchanged); cmd/controller/run.go is the only place this
// gets constructed and wired in.
type JobRunner struct {
	Client    kubernetes.Interface
	Namespace string

	// ImagePullSecrets names existing Secrets (in Namespace) kubelet uses
	// to authenticate pulling a private runner image — see
	// k8sjob.RunRequest.ImagePullSecrets. Cluster-wide: set once at
	// startup from HyveConfig.spec.imagePullSecrets (cmd/controller/run.go),
	// not per-module — the same registry credentials apply regardless of
	// which module's Job is pulling from it.
	ImagePullSecrets []string

	// PollInterval and Timeout control how long Run waits for a Job to
	// finish before giving up. Zero values fall back to k8sjob's own
	// defaults (2s / 15m).
	PollInterval time.Duration
	Timeout      time.Duration

	// ImageInstalls is set once at startup from HyveConfig.spec.imageInstalls
	// (cmd/controller/run.go) and passed through to every k8sjob.Run call —
	// see k8sjob.ImageInstall's own doc comment.
	ImageInstalls []k8sjob.ImageInstall
}

// Run executes script (the module operation script's own content, not a
// path — the dispatched Job's container has no access to the caller's
// filesystem) inside a fresh Job running image, with env applied.
func (r *JobRunner) Run(ctx context.Context, name, image, script string, env []string) (stdout string, exitCode int, err error) {
	return k8sjob.Run(ctx, r.Client, k8sjob.RunRequest{
		Name:             name,
		Namespace:        r.Namespace,
		Image:            image,
		Script:           script,
		Env:              env,
		ImagePullSecrets: r.ImagePullSecrets,
		PollInterval:     r.PollInterval,
		Timeout:          r.Timeout,
		ImageInstalls:    r.ImageInstalls,
	}, nil)
}
