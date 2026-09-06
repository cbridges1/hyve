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
	"encoding/base64"
	"fmt"
	"io"
	"log"
	"os"
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

	// ImagePullSecrets names existing kubernetes.io/dockerconfigjson
	// Secrets (in Namespace) kubelet should use to authenticate pulling
	// Image — set on the Job's PodSpec exactly like a normal Pod's own
	// spec.imagePullSecrets. Needed for a private Image; a public one needs
	// nothing here. The Secret itself isn't created or read by this
	// package — it must already exist in Namespace (see
	// HyveConfigSpec.ImagePullSecrets for cluster mode's cluster-wide
	// configuration of this list).
	ImagePullSecrets []string

	// PollInterval and Timeout control how long Run waits for the Job to
	// finish before giving up. Zero values fall back to defaultPollInterval
	// / defaultTimeout — set explicitly in tests for a fast, deterministic
	// poll loop. Timeout (or its default) is also used as the Job's own
	// ActiveDeadlineSeconds — see that field's own doc comment in Run.
	PollInterval time.Duration
	Timeout      time.Duration

	// ImageInstalls is passed through from HyveConfig.spec.imageInstalls by
	// the caller (module.JobRunner / workflow.KubernetesJobStepRunner) —
	// see ImageInstall's own doc comment. Run matches these against the
	// final resolved image (after Image's own empty-string fallback, if
	// any) and prefixes the first match's Install script onto the Job's
	// script. At most one entry is expected to match a given image; if
	// more than one does, the first match (in slice order) wins.
	ImageInstalls []ImageInstall
}

// ImageInstall pairs an exact image reference with a shell script to run
// once, before a Job's own script, inside that same container — see
// HyveConfigSpec.ImageInstalls' own doc comment for why this is declared
// per image rather than per module/workflow.
type ImageInstall struct {
	Image   string
	Install string
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

// buildLocalObjectRefs returns nil (not an empty slice) for a nil/empty
// input, matching corev1.PodSpec.ImagePullSecrets' own omitempty
// convention and avoiding a spurious empty imagePullSecrets: [] in every
// created Job's YAML when nothing was configured.
func buildLocalObjectRefs(names []string) []corev1.LocalObjectReference {
	if len(names) == 0 {
		return nil
	}
	refs := make([]corev1.LocalObjectReference, len(names))
	for i, n := range names {
		refs[i] = corev1.LocalObjectReference{Name: n}
	}
	return refs
}

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

// defaultFallbackImage is used when req.Image is empty after every
// configured tier has been exhausted (ClusterDefinition.spec.runner.image /
// HyveConfig.spec.defaultModuleImage for modules; step/job container: /
// HyveConfig.spec.defaultWorkflowImage for workflows — see those fields'
// doc comments). A small, official, apt-based image so an
// HyveConfig.spec.imageInstalls entry for it has something to
// `apt-get install` into out of the box. Does NOT guarantee curl or
// ca-certificates are present (Debian's slim images are commonly missing
// both) — an install script needing network access installs those itself
// first. A deliberate, narrow exception to
// HyveConfigSpec.DefaultModuleImage/DefaultWorkflowImage's own "never a
// code-level default" stance: an out-of-the-box cluster-mode install
// should work with zero image configuration, paired with an
// imageInstalls entry declared for this exact fallback image.
const defaultFallbackImage = "debian:stable-slim"

// defaultPollInterval and defaultTimeout are RunRequest.PollInterval/
// Timeout's zero-value fallbacks — named constants so Run's use of Timeout
// for ActiveDeadlineSeconds and waitForJob's own identical fallback can't
// silently drift apart.
const (
	defaultPollInterval = 2 * time.Second
	defaultTimeout      = 15 * time.Minute
)

// jobTTLSecondsAfterFinished is a backstop, not the primary cleanup path —
// Run's own deferred Delete call (below) still fires immediately in the
// normal case. This exists for when that never gets to run at all: if the
// process calling Run is killed (crashed, OOM-killed, or a Deployment
// scaled to 0) between the Job finishing and Run's defer executing, the
// Job is otherwise orphaned forever — nothing else in this codebase sweeps
// for leftover hyve.io/run-labeled Jobs. Confirmed live: exactly this
// happened when a controller pod was stopped mid-verification, leaving two
// already-finished Jobs sitting in the cluster with no process left to
// clean them up. Counts from the Job's own completion (Succeeded/Failed),
// not creation — a still-*running* Job when the caller disappears is a
// separate, unhandled risk this does not cover (see ActiveDeadlineSeconds
// as a possible follow-up if that ever needs bounding too).
const jobTTLSecondsAfterFinished = int32(300)

// Run creates a Job running req.Image with req.Script as its entrypoint,
// waits for it to finish, and returns its pod's combined logs. The Job
// (and its Pods, via Kubernetes' own Job-owns-Pods garbage collection) is
// deleted afterward regardless of outcome — nothing is left behind for a
// caller to separately clean up. If output is non-nil, the pod's logs are
// also streamed to it as they're captured.
func Run(ctx context.Context, client kubernetes.Interface, req RunRequest, output io.Writer) (stdout string, exitCode int, err error) {
	if req.Script == "" {
		return "", 0, fmt.Errorf("no command or script specified for %q", req.Name)
	}

	image := req.Image
	if image == "" {
		image = defaultFallbackImage
		log.Printf("hyve: %q has no configured container image — falling back to %s", req.Name, defaultFallbackImage)
	}

	script := req.Script
	for _, ii := range req.ImageInstalls {
		if ii.Image == image && ii.Install != "" {
			script = fmt.Sprintf("if ! (\n%s\n); then\n  echo \"hyve: image %q install script failed\" >&2\n  exit 1\nfi\n%s", ii.Install, image, script)
			break
		}
	}

	reqEnv, script := inlineLocalKubeconfig(req.Env, script)

	envVars := make([]corev1.EnvVar, 0, len(reqEnv))
	for _, kv := range reqEnv {
		if idx := strings.IndexByte(kv, '='); idx > 0 {
			envVars = append(envVars, corev1.EnvVar{Name: kv[:idx], Value: kv[idx+1:]})
		}
	}

	// effectiveTimeout is resolved here, once, and used for both
	// ActiveDeadlineSeconds (below) and the wait loop (waitForJob) — the
	// same value governs "how long Run itself will wait" and "how long
	// Kubernetes lets the Job's pod run before force-failing it," so a
	// caller that gives up waiting was never going to see a later result
	// anyway. Without this, a Job whose caller process died (crashed, or a
	// Deployment scaled to 0 — see jobTTLSecondsAfterFinished's own doc
	// comment for exactly this scenario, confirmed live) could keep
	// running indefinitely with nothing left tracking it or able to react
	// to its outcome; ActiveDeadlineSeconds bounds that even when nothing
	// is left alive to call Delete.
	effectiveTimeout := req.Timeout
	if effectiveTimeout <= 0 {
		effectiveTimeout = defaultTimeout
	}
	activeDeadlineSeconds := int64(effectiveTimeout.Seconds())

	backoffLimit := int32(0) // no retries — the caller's own retry/continueOnError semantics own that decision, not the Job's
	ttl := jobTTLSecondsAfterFinished
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      sanitizeJobName(req.Name),
			Namespace: req.Namespace,
			Labels:    map[string]string{"app.kubernetes.io/managed-by": "hyve", "hyve.io/run": req.Name},
		},
		Spec: batchv1.JobSpec{
			BackoffLimit:            &backoffLimit,
			TTLSecondsAfterFinished: &ttl,
			ActiveDeadlineSeconds:   &activeDeadlineSeconds,
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					RestartPolicy:    corev1.RestartPolicyNever,
					ImagePullSecrets: buildLocalObjectRefs(req.ImagePullSecrets),
					Containers: []corev1.Container{{
						Name:       "run",
						Image:      image,
						Command:    []string{"/bin/sh", "-c", script},
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

	// Primary cleanup path — fires immediately once Run itself is about to
	// return. jobTTLSecondsAfterFinished (set on the Job's own spec above)
	// is the backstop for when this defer never gets to run at all.
	cleanupCtx, cancelCleanup := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelCleanup()
	defer func() {
		policy := metav1.DeletePropagationBackground
		_ = client.BatchV1().Jobs(req.Namespace).Delete(cleanupCtx, created.Name, metav1.DeleteOptions{PropagationPolicy: &policy})
	}()

	finished, waitErr := waitForJob(ctx, client, req.Namespace, created.Name, req.PollInterval, effectiveTimeout)

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

// inlineKubeconfigJobPath is where the preamble inlineLocalKubeconfig
// injects writes the materialized kubeconfig inside the dispatched Job's
// own container — arbitrary, just needs to be a writable path no step
// script is likely to collide with.
const inlineKubeconfigJobPath = "/tmp/hyve-kubeconfig/config"

// inlineLocalKubeconfig rewrites a KUBECONFIG=<path> entry in env into
// something the Job it's about to be attached to can actually use.
//
// Run always executes in the controller's own pod, but the Job it creates
// gets a brand new pod with its own, entirely separate filesystem — no
// volume shared with the controller. A KUBECONFIG env var naming a path on
// the controller's local disk (written earlier by that cluster's module
// auth operation — see module.KubeconfigPathForCluster) is therefore
// meaningless inside the Job: confirmed live, kubectl found no file at that
// path and silently fell back to the Job pod's own in-cluster
// ServiceAccount instead of erroring, authenticating against the wrong
// cluster entirely (the one hosting hyve itself, not the intended target).
//
// The fix: read the file's bytes here, where they're actually reachable,
// and thread its *content* through instead — base64-encoded to survive
// shell quoting untouched, written back out to a real file by a small
// preamble that runs before the caller's own script, inside the Job's own
// container. Mirrors the ImageInstalls prepend just above this function's
// only call site, and the same base64-relay idiom module.Executor's own
// dispatched-auth wrapper already uses for the reverse direction (getting a
// kubeconfig's bytes *out* of a Job).
//
// If the KUBECONFIG value isn't a readable local file, it's left
// untouched — most callers (module status/create/delete, or a workflow
// step with no secretsFrom/auth dependency at all) never set KUBECONFIG in
// the first place, and a future caller that already deliberately points it
// at a path baked into its own image shouldn't be broken by this.
func inlineLocalKubeconfig(env []string, script string) ([]string, string) {
	const prefix = "KUBECONFIG="
	out := make([]string, 0, len(env))
	var kcPath string
	for _, kv := range env {
		if strings.HasPrefix(kv, prefix) {
			kcPath = strings.TrimPrefix(kv, prefix)
			continue
		}
		out = append(out, kv)
	}
	if kcPath == "" {
		return env, script
	}
	content, err := os.ReadFile(kcPath)
	if err != nil {
		// Not inlinable (already a container-local path, or simply gone) —
		// pass the original entry through unchanged rather than dropping it
		// silently; whatever previously happened when KUBECONFIG couldn't
		// be resolved still happens, no worse off than before this fix.
		return env, script
	}
	out = append(out, "HYVE_KUBECONFIG_B64="+base64.StdEncoding.EncodeToString(content))
	preamble := fmt.Sprintf(
		"mkdir -p %q && echo \"$HYVE_KUBECONFIG_B64\" | base64 -d > %q && export KUBECONFIG=%q || { echo \"hyve: failed to materialize kubeconfig for job dispatch\" >&2; exit 1; }\n",
		strings.TrimSuffix(inlineKubeconfigJobPath, "/config"), inlineKubeconfigJobPath, inlineKubeconfigJobPath,
	)
	return out, preamble + script
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
		interval = defaultPollInterval
	}
	if timeout <= 0 {
		timeout = defaultTimeout
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
