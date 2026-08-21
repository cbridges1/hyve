package workflow

import (
	"context"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"k8s.io/client-go/kubernetes"

	"github.com/cbridges1/hyve/internal/k8sjob"
)

// KubernetesJobStepRunner runs each workflow step as a short-lived
// batch/v1.Job — controller mode's answer to "the machine running the
// workflow is now the controller's own long-running pod, which can't
// reasonably carry every tool every driver module's workflow might invoke,
// and running arbitrary scripts directly inside the controller's own
// process is an isolation regression versus today's already-short-lived
// per-invocation CLI process." Uses client-go's typed Interface (not a
// controller-runtime client) — Jobs/Pods/pod-logs is exactly the shape
// client-go's clientset (and its fake for tests) is built for.
//
// The actual Job create/wait/log-fetch/cleanup lifecycle lives in
// internal/k8sjob, shared with module.JobRunner — this type only resolves
// step-specific image/script/env and translates k8sjob's result back into
// the StepRunner interface's shape.
type KubernetesJobStepRunner struct {
	Client    kubernetes.Interface
	Namespace string

	// ImagePullSecrets names existing Secrets (in Namespace) kubelet uses
	// to authenticate pulling a private step container: image — see
	// k8sjob.RunRequest.ImagePullSecrets. Cluster-wide: set once at
	// startup from HyveConfig.spec.imagePullSecrets (cmd/controller/run.go).
	ImagePullSecrets []string

	// PollInterval and Timeout control how long RunStep waits for a Job to
	// finish before giving up. Zero values fall back to sane defaults (2s
	// / 15m) — set explicitly in tests for a fast, deterministic poll loop.
	PollInterval time.Duration
	Timeout      time.Duration
}

// RequiresContainer always returns true — see StepRunner's doc comment.
func (KubernetesJobStepRunner) RequiresContainer() bool { return true }

var jobNameSanitizer = regexp.MustCompile(`[^a-z0-9-]+`)

// maxJobNameLen is the DNS-1123 label limit (63 chars) — Job names must
// stay within it since the Job controller also stamps `job-name=<name>`
// onto its Pods as a label value, stricter than a plain object name.
const maxJobNameLen = 63

// sanitizeJobName documents/tests this runner's Job-naming contract
// (exercised directly by TestSanitizeJobName). k8sjob.Run performs the
// actual sanitization of whatever Name it's given internally using the same
// scheme, so this isn't called by RunStep itself — kept as the
// workflow-specific naming contract (its own "step" fallback for an empty
// name, versus k8sjob's generic "run") independent of that.
// maxStepNamePortion leaves room for the "hyve-" prefix (5 chars) and
// "-<UnixNano timestamp>" suffix (up to 20 chars: a leading "-" plus a
// 19-digit nanosecond epoch) within maxJobNameLen — confirmed live: an
// earlier version of this budget only accounted for the prefix, truncating
// the name portion to 40 chars and producing a 65-char result, over the
// limit.
const maxStepNamePortion = maxJobNameLen - len("hyve-") - len("-") - len("9223372036854775807") // 19-digit max int64 (UnixNano)

func sanitizeJobName(stepName string) string {
	s := jobNameSanitizer.ReplaceAllString(strings.ToLower(stepName), "-")
	s = strings.Trim(s, "-")
	if s == "" {
		s = "step"
	}
	if len(s) > maxStepNamePortion {
		s = strings.Trim(s[:maxStepNamePortion], "-")
	}
	return fmt.Sprintf("hyve-%s-%d", s, time.Now().UnixNano())
}

// RunStep creates a Job running step.Container with step.Script (or
// step.Command) as its entrypoint, waits for it to finish, and returns its
// pod's combined logs. The Job (and its Pods, via Kubernetes' own
// Job-owns-Pods garbage collection) is deleted afterward regardless of
// outcome — nothing is left behind for a caller to separately clean up.
func (r *KubernetesJobStepRunner) RunStep(ctx context.Context, step WorkflowStep, env []string, workingDir string, output io.Writer) (stdout, stderr string, exitCode int, err error) {
	if step.Container == "" {
		return "", "", 0, fmt.Errorf("step %q resolved to no container image — set container: on the step, its job, or HyveConfig.spec.defaultWorkflowImage", step.Name)
	}

	script := step.Script
	if script == "" {
		script = step.Command
	}
	if script == "" {
		return "", "", 0, fmt.Errorf("no command or script specified")
	}

	logs, code, runErr := k8sjob.Run(ctx, r.Client, k8sjob.RunRequest{
		Name:             step.Name,
		Namespace:        r.Namespace,
		Image:            step.Container,
		Script:           script,
		Env:              env,
		WorkingDir:       workingDir,
		ImagePullSecrets: r.ImagePullSecrets,
		PollInterval:     r.PollInterval,
		Timeout:          r.Timeout,
	}, output)
	if runErr != nil {
		return logs, "", code, fmt.Errorf("step %q: %w", step.Name, runErr)
	}
	return logs, "", code, nil
}
