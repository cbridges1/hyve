package k8sjob

import (
	"context"
	"fmt"
	"log"
	"strings"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
)

// PushJobRequest describes a Job that reports its own result by actively
// pushing it somewhere (an HTTP callback the script itself calls, see
// internal/api's access-method mint handler) rather than by the caller
// polling Job status and reading pod logs the way Run does. Built for
// exactly one case so far — an AccessMethod driver module's auth
// operation, whose result (a kubeconfig) must never sit in a log stream —
// but nothing here is specific to that; any script/image/env-in
// combination works.
type PushJobRequest struct {
	Name      string
	Namespace string
	Image     string
	Script    string
	Env       []string // "KEY=VALUE" — non-sensitive only; see SecretEnvFromName for anything that isn't

	// SecretEnvFromName, if set, names a Secret (in Namespace) the Job's
	// container loads its entire key set from via envFrom — the intended
	// home for anything sensitive (a credential), kept out of the pod
	// spec's own (widely-readable) env list entirely. The Secret need not
	// exist yet when PushJob is called: Kubernetes doesn't validate an
	// envFrom.secretRef at admission time, only at container start, so the
	// normal caller sequence is PushJob first (to learn the created Job's
	// UID), then create the Secret with an ownerReference pointing at that
	// Job — see this package's own doc comment on why that ordering is
	// safe. A pod whose Secret never appears simply stays in
	// CreateContainerConfigError until ActiveDeadlineSeconds kills it.
	SecretEnvFromName string

	ImagePullSecrets []string
	ImageInstalls    []ImageInstall

	// ActiveDeadlineSeconds bounds how long the Job's pod may run before
	// Kubernetes force-fails it — the caller's own wait timeout (it is
	// not this package's job to wait; see PushJob's own doc comment)
	// should stay well under this, so a caller that gives up is never
	// left wondering whether the Job might still complete later. Falls
	// back to defaultTimeout's own seconds value, same as Run, if unset.
	ActiveDeadlineSeconds int64
}

// PushJob creates a Job running req.Script and returns immediately — it
// does NOT wait for the Job to finish, unlike Run, and never reads its pod
// logs. The caller is expected to be waiting on its own out-of-band signal
// (an HTTP push from inside the script, in the one caller that exists
// today) and is responsible for deleting the Job once it has what it
// needs (TTLSecondsAfterFinished is still set as a backstop, matching
// Run's own jobTTLSecondsAfterFinished precedent, for the case that never
// happens — caller process crashes, etc.).
//
// Returns the created Job's name and UID — UID is what a caller building
// an owner-referenced companion object (see SecretEnvFromName) needs to
// populate that reference correctly.
func PushJob(ctx context.Context, client kubernetes.Interface, req PushJobRequest) (name string, uid types.UID, err error) {
	if req.Script == "" {
		return "", "", fmt.Errorf("no script specified for %q", req.Name)
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

	envVars := make([]corev1.EnvVar, 0, len(req.Env))
	for _, kv := range req.Env {
		if idx := strings.IndexByte(kv, '='); idx > 0 {
			envVars = append(envVars, corev1.EnvVar{Name: kv[:idx], Value: kv[idx+1:]})
		}
	}

	var envFrom []corev1.EnvFromSource
	if req.SecretEnvFromName != "" {
		envFrom = []corev1.EnvFromSource{{SecretRef: &corev1.SecretEnvSource{
			LocalObjectReference: corev1.LocalObjectReference{Name: req.SecretEnvFromName},
		}}}
	}

	activeDeadline := req.ActiveDeadlineSeconds
	if activeDeadline <= 0 {
		activeDeadline = int64(defaultTimeout.Seconds())
	}

	backoffLimit := int32(0) // no retries — a retry would re-run the auth operation with a second set of credentials-in-a-Secret lifecycle to reason about, not worth it for this use case
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
			ActiveDeadlineSeconds:   &activeDeadline,
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					RestartPolicy:    corev1.RestartPolicyNever,
					ImagePullSecrets: buildLocalObjectRefs(req.ImagePullSecrets),
					Containers: []corev1.Container{{
						Name:    "run",
						Image:   image,
						Command: []string{"/bin/sh", "-c", script},
						Env:     envVars,
						EnvFrom: envFrom,
					}},
				},
			},
		},
	}

	created, err := client.BatchV1().Jobs(req.Namespace).Create(ctx, job, metav1.CreateOptions{})
	if err != nil {
		return "", "", fmt.Errorf("create job for %q: %w", req.Name, err)
	}
	return created.Name, created.UID, nil
}
