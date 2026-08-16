package workflow

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
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
type KubernetesJobStepRunner struct {
	Client    kubernetes.Interface
	Namespace string

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

// sanitizeJobName produces a DNS-1123-safe, reasonably unique Job name from
// a step name. maxStepNamePortion below leaves room for the "hyve-" prefix
// (5 chars) and "-<UnixNano timestamp>" suffix (up to 20 chars: a leading
// "-" plus a 19-digit nanosecond epoch) within maxJobNameLen — confirmed
// live: an earlier version of this budget only accounted for the prefix,
// truncating the name portion to 40 chars and producing a 65-char result,
// over the limit.
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

	envVars := make([]corev1.EnvVar, 0, len(env))
	for _, kv := range env {
		if idx := strings.IndexByte(kv, '='); idx > 0 {
			envVars = append(envVars, corev1.EnvVar{Name: kv[:idx], Value: kv[idx+1:]})
		}
	}

	backoffLimit := int32(0) // no retries — Executor's own retry/continueOnError semantics own that decision, not the Job's
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      sanitizeJobName(step.Name),
			Namespace: r.Namespace,
			Labels:    map[string]string{"app.kubernetes.io/managed-by": "hyve", "hyve.io/step": step.Name},
		},
		Spec: batchv1.JobSpec{
			BackoffLimit: &backoffLimit,
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					RestartPolicy: corev1.RestartPolicyNever,
					Containers: []corev1.Container{{
						Name:       "step",
						Image:      step.Container,
						Command:    []string{"/bin/sh", "-c", script},
						Env:        envVars,
						WorkingDir: workingDir, // best-effort — only meaningful if step.Container has this path baked in; no host/repo checkout is mounted here
					}},
				},
			},
		},
	}

	created, err := r.Client.BatchV1().Jobs(r.Namespace).Create(ctx, job, metav1.CreateOptions{})
	if err != nil {
		return "", "", 0, fmt.Errorf("create job for step %q: %w", step.Name, err)
	}

	cleanupCtx, cancelCleanup := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelCleanup()
	defer func() {
		policy := metav1.DeletePropagationBackground
		_ = r.Client.BatchV1().Jobs(r.Namespace).Delete(cleanupCtx, created.Name, metav1.DeleteOptions{PropagationPolicy: &policy})
	}()

	finished, waitErr := r.waitForJob(ctx, created.Name)

	logs, logErr := r.fetchPodLogs(ctx, created.Name)
	if logErr == nil && logs != "" {
		if output != nil {
			_, _ = io.WriteString(output, logs)
		}
	}

	if waitErr != nil {
		return logs, "", 0, waitErr
	}

	code := 0
	var runErr error
	if !finished.succeeded {
		code = finished.exitCode
		if code == 0 {
			code = 1
		}
		runErr = fmt.Errorf("step %q failed (job %s, exit code %d)", step.Name, created.Name, code)
	}
	return logs, "", code, runErr
}

type jobOutcome struct {
	succeeded bool
	exitCode  int
}

// waitForJob polls the Job (and, once it exists, its Pod's container
// status for an exit code) until it reaches a terminal state or ctx/the
// configured Timeout expires.
func (r *KubernetesJobStepRunner) waitForJob(ctx context.Context, jobName string) (jobOutcome, error) {
	interval := r.PollInterval
	if interval <= 0 {
		interval = 2 * time.Second
	}
	timeout := r.Timeout
	if timeout <= 0 {
		timeout = 15 * time.Minute
	}

	deadline := time.Now().Add(timeout)
	for {
		job, err := r.Client.BatchV1().Jobs(r.Namespace).Get(ctx, jobName, metav1.GetOptions{})
		if err != nil {
			return jobOutcome{}, fmt.Errorf("get job %s: %w", jobName, err)
		}
		if job.Status.Succeeded > 0 {
			return jobOutcome{succeeded: true}, nil
		}
		if job.Status.Failed > 0 {
			return jobOutcome{succeeded: false, exitCode: r.podExitCode(ctx, jobName)}, nil
		}
		if time.Now().After(deadline) {
			return jobOutcome{}, fmt.Errorf("timed out after %s waiting for job %s", timeout, jobName)
		}
		select {
		case <-ctx.Done():
			return jobOutcome{}, ctx.Err()
		case <-time.After(interval):
		}
	}
}

// podExitCode best-effort resolves the failed container's exit code from
// the Job's Pod. Returns 0 (caller defaults to 1) if it can't be
// determined — the Job having Status.Failed > 0 is already authoritative
// enough to know the step failed even without a precise code.
func (r *KubernetesJobStepRunner) podExitCode(ctx context.Context, jobName string) int {
	pod := r.findPod(ctx, jobName)
	if pod == nil {
		return 0
	}
	for _, cs := range pod.Status.ContainerStatuses {
		if cs.State.Terminated != nil {
			return int(cs.State.Terminated.ExitCode)
		}
	}
	return 0
}

func (r *KubernetesJobStepRunner) findPod(ctx context.Context, jobName string) *corev1.Pod {
	pods, err := r.Client.CoreV1().Pods(r.Namespace).List(ctx, metav1.ListOptions{
		LabelSelector: "job-name=" + jobName,
	})
	if err != nil || len(pods.Items) == 0 {
		return nil
	}
	return &pods.Items[0]
}

// fetchPodLogs returns the Job's pod's combined stdout+stderr (a single
// container per pod, per the Job spec RunStep builds — GetLogs already
// returns one combined stream by default, same reality LocalStepRunner's
// own single-buffer capture reflects).
func (r *KubernetesJobStepRunner) fetchPodLogs(ctx context.Context, jobName string) (string, error) {
	pod := r.findPod(ctx, jobName)
	if pod == nil {
		return "", nil
	}
	req := r.Client.CoreV1().Pods(r.Namespace).GetLogs(pod.Name, &corev1.PodLogOptions{})
	stream, err := req.Stream(ctx)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return "", nil
		}
		return "", fmt.Errorf("stream logs for pod %s: %w", pod.Name, err)
	}
	defer stream.Close()
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, stream); err != nil {
		return buf.String(), fmt.Errorf("read logs for pod %s: %w", pod.Name, err)
	}
	return buf.String(), nil
}
