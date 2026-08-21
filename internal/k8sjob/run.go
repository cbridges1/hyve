// Package k8sjob is the shared one-shot batch/v1.Job lifecycle primitive
// underneath both internal/workflow.KubernetesJobStepRunner (workflow step
// execution) and internal/module.JobRunner (module create/status/delete/
// auth execution) — the two are the identical operation (run this image
// with this script and env, capture combined stdout+stderr, report an exit
// code, clean up regardless of outcome) with different callers deciding
// what image/name/env to use, so this is extracted once rather than
// duplicated per package.
package k8sjob

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

// RunRequest describes one one-shot Job to run.
type RunRequest struct {
	// Name identifies the run for the generated Job's name (sanitized and
	// made unique internally — need not be DNS-1123-safe or unique itself).
	Name       string
	Namespace  string
	Image      string
	Script     string
	Env        []string // "KEY=VALUE" pairs
	WorkingDir string   // best-effort — only meaningful if Image has this path baked in; no checkout is mounted

	// PollInterval and Timeout control how long Run waits for the Job to
	// finish before giving up. Zero values fall back to sane defaults (2s
	// / 15m) — set explicitly in tests for a fast, deterministic poll loop.
	PollInterval time.Duration
	Timeout      time.Duration
}

var jobNameSanitizer = regexp.MustCompile(`[^a-z0-9-]+`)

// maxJobNameLen is the DNS-1123 label limit (63 chars) — Job names must
// stay within it since the Job controller also stamps `job-name=<name>`
// onto its Pods as a label value, stricter than a plain object name.
const maxJobNameLen = 63

// maxNamePortion leaves room for the "hyve-" prefix (5 chars) and
// "-<UnixNano timestamp>" suffix (up to 20 chars: a leading "-" plus a
// 19-digit nanosecond epoch) within maxJobNameLen.
const maxNamePortion = maxJobNameLen - len("hyve-") - len("-") - len("9223372036854775807") // 19-digit max int64 (UnixNano)

func sanitizeJobName(name string) string {
	s := jobNameSanitizer.ReplaceAllString(strings.ToLower(name), "-")
	s = strings.Trim(s, "-")
	if s == "" {
		s = "run"
	}
	if len(s) > maxNamePortion {
		s = strings.Trim(s[:maxNamePortion], "-")
	}
	return fmt.Sprintf("hyve-%s-%d", s, time.Now().UnixNano())
}

// Run creates a Job running req.Image with req.Script as its entrypoint,
// waits for it to finish, and returns its pod's combined logs. The Job
// (and its Pods, via Kubernetes' own Job-owns-Pods garbage collection) is
// deleted afterward regardless of outcome — nothing is left behind for a
// caller to separately clean up. If output is non-nil, the pod's logs are
// also streamed to it as they're captured.
func Run(ctx context.Context, client kubernetes.Interface, req RunRequest, output io.Writer) (stdout string, exitCode int, err error) {
	if req.Image == "" {
		return "", 0, fmt.Errorf("%q resolved to no container image", req.Name)
	}
	if req.Script == "" {
		return "", 0, fmt.Errorf("no command or script specified for %q", req.Name)
	}

	envVars := make([]corev1.EnvVar, 0, len(req.Env))
	for _, kv := range req.Env {
		if idx := strings.IndexByte(kv, '='); idx > 0 {
			envVars = append(envVars, corev1.EnvVar{Name: kv[:idx], Value: kv[idx+1:]})
		}
	}

	backoffLimit := int32(0) // no retries — the caller's own retry/continueOnError semantics own that decision, not the Job's
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      sanitizeJobName(req.Name),
			Namespace: req.Namespace,
			Labels:    map[string]string{"app.kubernetes.io/managed-by": "hyve", "hyve.io/run": req.Name},
		},
		Spec: batchv1.JobSpec{
			BackoffLimit: &backoffLimit,
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					RestartPolicy: corev1.RestartPolicyNever,
					Containers: []corev1.Container{{
						Name:       "run",
						Image:      req.Image,
						Command:    []string{"/bin/sh", "-c", req.Script},
						Env:        envVars,
						WorkingDir: req.WorkingDir,
					}},
				},
			},
		},
	}

	created, err := client.BatchV1().Jobs(req.Namespace).Create(ctx, job, metav1.CreateOptions{})
	if err != nil {
		return "", 0, fmt.Errorf("create job for %q: %w", req.Name, err)
	}

	cleanupCtx, cancelCleanup := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelCleanup()
	defer func() {
		policy := metav1.DeletePropagationBackground
		_ = client.BatchV1().Jobs(req.Namespace).Delete(cleanupCtx, created.Name, metav1.DeleteOptions{PropagationPolicy: &policy})
	}()

	finished, waitErr := waitForJob(ctx, client, req.Namespace, created.Name, req.PollInterval, req.Timeout)

	logs, logErr := fetchPodLogs(ctx, client, req.Namespace, created.Name)
	if logErr == nil && logs != "" && output != nil {
		_, _ = io.WriteString(output, logs)
	}

	if waitErr != nil {
		return logs, 0, waitErr
	}

	code := 0
	var runErr error
	if !finished.succeeded {
		code = finished.exitCode
		if code == 0 {
			code = 1
		}
		runErr = fmt.Errorf("%q failed (job %s, exit code %d)", req.Name, created.Name, code)
	}
	return logs, code, runErr
}

type jobOutcome struct {
	succeeded bool
	exitCode  int
}

// waitForJob polls the Job (and, once it exists, its Pod's container
// status for an exit code) until it reaches a terminal state or ctx/the
// configured timeout expires.
func waitForJob(ctx context.Context, client kubernetes.Interface, namespace, jobName string, pollInterval, timeout time.Duration) (jobOutcome, error) {
	interval := pollInterval
	if interval <= 0 {
		interval = 2 * time.Second
	}
	if timeout <= 0 {
		timeout = 15 * time.Minute
	}

	deadline := time.Now().Add(timeout)
	for {
		job, err := client.BatchV1().Jobs(namespace).Get(ctx, jobName, metav1.GetOptions{})
		if err != nil {
			return jobOutcome{}, fmt.Errorf("get job %s: %w", jobName, err)
		}
		if job.Status.Succeeded > 0 {
			return jobOutcome{succeeded: true}, nil
		}
		if job.Status.Failed > 0 {
			return jobOutcome{succeeded: false, exitCode: podExitCode(ctx, client, namespace, jobName)}, nil
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
// enough to know the run failed even without a precise code.
func podExitCode(ctx context.Context, client kubernetes.Interface, namespace, jobName string) int {
	pod := findPod(ctx, client, namespace, jobName)
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

func findPod(ctx context.Context, client kubernetes.Interface, namespace, jobName string) *corev1.Pod {
	pods, err := client.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: "job-name=" + jobName,
	})
	if err != nil || len(pods.Items) == 0 {
		return nil
	}
	return &pods.Items[0]
}

// fetchPodLogs returns the Job's pod's combined stdout+stderr (a single
// container per pod, per the Job spec Run builds).
func fetchPodLogs(ctx context.Context, client kubernetes.Interface, namespace, jobName string) (string, error) {
	pod := findPod(ctx, client, namespace, jobName)
	if pod == nil {
		return "", nil
	}
	req := client.CoreV1().Pods(namespace).GetLogs(pod.Name, &corev1.PodLogOptions{})
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
