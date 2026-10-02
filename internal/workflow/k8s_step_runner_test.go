package workflow

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sfake "k8s.io/client-go/kubernetes/fake"

	"github.com/cbridges1/hyve/internal/k8sjob"
)

func TestKubernetesJobStepRunner_RequiresContainer(t *testing.T) {
	assert.True(t, KubernetesJobStepRunner{}.RequiresContainer())
}

// TestKubernetesJobStepRunner_NoContainerFallsBackToDefaultImage confirms
// an empty step.Container no longer hard-fails pre-flight — it's passed
// straight through to k8sjob.Run, which falls back to its own default
// image (see k8sjob's defaultFallbackImage) rather than erroring.
func TestKubernetesJobStepRunner_NoContainerFallsBackToDefaultImage(t *testing.T) {
	clientset := k8sfake.NewClientset()
	r := &KubernetesJobStepRunner{
		Client:       clientset,
		Namespace:    "default",
		PollInterval: 10 * time.Millisecond,
		Timeout:      5 * time.Second,
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		var jobName string
		require.Eventually(t, func() bool {
			jobs, err := clientset.BatchV1().Jobs("default").List(context.Background(), metav1.ListOptions{})
			if err != nil || len(jobs.Items) == 0 {
				return false
			}
			jobName = jobs.Items[0].Name
			return true
		}, 2*time.Second, 5*time.Millisecond, "job was never created")

		job, err := clientset.BatchV1().Jobs("default").Get(context.Background(), jobName, metav1.GetOptions{})
		require.NoError(t, err)
		require.Len(t, job.Spec.Template.Spec.Containers, 1)
		assert.Equal(t, "debian:stable-slim", job.Spec.Template.Spec.Containers[0].Image)

		job.Status.Succeeded = 1
		_, err = clientset.BatchV1().Jobs("default").UpdateStatus(context.Background(), job, metav1.UpdateOptions{})
		require.NoError(t, err)
	}()

	_, _, exitCode, err := r.RunStep(context.Background(), WorkflowStep{Name: "step", Script: "echo hi"}, nil, "", nil)
	<-done
	require.NoError(t, err)
	assert.Equal(t, 0, exitCode)
}

func TestKubernetesJobStepRunner_NoScriptOrCommandIsAHardError(t *testing.T) {
	r := &KubernetesJobStepRunner{Client: k8sfake.NewClientset(), Namespace: "default"}
	_, _, _, err := r.RunStep(context.Background(), WorkflowStep{Name: "step", Container: "alpine:3"}, nil, "", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no command or script")
}

// TestKubernetesJobStepRunner_CreatesJobWithExpectedSpec drives RunStep
// against a fake clientset with the poll interval set fast, and a
// background goroutine that simulates the Job controller: marks the Job
// Succeeded and creates a labeled Pod once the Job exists. Verifies the
// created Job's spec matches what a real cluster would need to actually
// run the step (image, entrypoint, env, no automatic retries).
func TestKubernetesJobStepRunner_CreatesJobWithExpectedSpecAndSucceeds(t *testing.T) {
	clientset := k8sfake.NewClientset()
	r := &KubernetesJobStepRunner{
		Client:       clientset,
		Namespace:    "default",
		PollInterval: 10 * time.Millisecond,
		Timeout:      5 * time.Second,
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		var jobName string
		require.Eventually(t, func() bool {
			jobs, err := clientset.BatchV1().Jobs("default").List(context.Background(), metav1.ListOptions{})
			if err != nil || len(jobs.Items) == 0 {
				return false
			}
			jobName = jobs.Items[0].Name
			return true
		}, 2*time.Second, 5*time.Millisecond, "job was never created")

		job, err := clientset.BatchV1().Jobs("default").Get(context.Background(), jobName, metav1.GetOptions{})
		require.NoError(t, err)

		// Assert the Job's spec is what a real cluster needs to run this
		// step correctly, before simulating its completion.
		require.Len(t, job.Spec.Template.Spec.Containers, 1)
		c := job.Spec.Template.Spec.Containers[0]
		assert.Equal(t, "my-image:latest", c.Image)
		assert.Equal(t, []string{"/bin/sh", "-c", "echo hello"}, c.Command)
		assert.Contains(t, c.Env, corev1.EnvVar{Name: "HYVE_CLUSTER_NAME", Value: "demo"})
		require.NotNil(t, job.Spec.BackoffLimit)
		assert.Equal(t, int32(0), *job.Spec.BackoffLimit, "no automatic Job retries — Executor's own retry/continueOnError semantics own that decision")

		_, err = clientset.CoreV1().Pods("default").Create(context.Background(), &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      jobName + "-pod",
				Namespace: "default",
				Labels:    map[string]string{"job-name": jobName},
			},
			Status: corev1.PodStatus{
				ContainerStatuses: []corev1.ContainerStatus{{
					Name:  "step",
					State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: 0}},
				}},
			},
		}, metav1.CreateOptions{})
		require.NoError(t, err)

		job.Status.Succeeded = 1
		_, err = clientset.BatchV1().Jobs("default").UpdateStatus(context.Background(), job, metav1.UpdateOptions{})
		require.NoError(t, err)
	}()

	stdout, stderr, exitCode, err := r.RunStep(context.Background(), WorkflowStep{
		Name:      "step",
		Container: "my-image:latest",
		Script:    "echo hello",
	}, []string{"HYVE_CLUSTER_NAME=demo"}, "/workdir", nil)

	<-done
	require.NoError(t, err)
	assert.Equal(t, 0, exitCode)
	assert.Empty(t, stderr)
	_ = stdout // fake clientset's GetLogs has no real content to serve; exercised for real in the kind smoke test instead

	// The Job (and, via Kubernetes' own GC, its Pod) is deleted afterward
	// regardless of outcome.
	jobs, err := clientset.BatchV1().Jobs("default").List(context.Background(), metav1.ListOptions{})
	require.NoError(t, err)
	assert.Empty(t, jobs.Items, "Job should be cleaned up after RunStep returns")
}

// TestKubernetesJobStepRunner_ImageInstalls_ThreadedThroughToJob confirms
// KubernetesJobStepRunner.RunStep passes its ImageInstalls through into
// the k8sjob.RunRequest it builds — set once at startup from
// HyveConfig.spec.imageInstalls (cmd/controller/run.go), reaching every
// workflow step's Job the same way ImagePullSecrets does.
func TestKubernetesJobStepRunner_ImageInstalls_ThreadedThroughToJob(t *testing.T) {
	clientset := k8sfake.NewClientset()
	r := &KubernetesJobStepRunner{
		Client: clientset, Namespace: "default",
		PollInterval:  10 * time.Millisecond,
		Timeout:       5 * time.Second,
		ImageInstalls: []k8sjob.ImageInstall{{Image: "my-image:latest", Install: "apt-get install -y jq"}},
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		var jobName string
		require.Eventually(t, func() bool {
			jobs, err := clientset.BatchV1().Jobs("default").List(context.Background(), metav1.ListOptions{})
			if err != nil || len(jobs.Items) == 0 {
				return false
			}
			jobName = jobs.Items[0].Name
			return true
		}, 2*time.Second, 5*time.Millisecond, "job was never created")

		job, err := clientset.BatchV1().Jobs("default").Get(context.Background(), jobName, metav1.GetOptions{})
		require.NoError(t, err)
		require.Len(t, job.Spec.Template.Spec.Containers, 1)
		assert.Contains(t, job.Spec.Template.Spec.Containers[0].Command[2], "apt-get install -y jq")

		job.Status.Succeeded = 1
		_, err = clientset.BatchV1().Jobs("default").UpdateStatus(context.Background(), job, metav1.UpdateOptions{})
		require.NoError(t, err)
	}()

	_, _, exitCode, err := r.RunStep(context.Background(), WorkflowStep{Name: "step", Container: "my-image:latest", Script: "echo hi"}, nil, "", nil)
	<-done
	require.NoError(t, err)
	assert.Equal(t, 0, exitCode)
}

func TestKubernetesJobStepRunner_JobFailureIsReportedAsAStepFailure(t *testing.T) {
	clientset := k8sfake.NewClientset()
	r := &KubernetesJobStepRunner{
		Client:       clientset,
		Namespace:    "default",
		PollInterval: 10 * time.Millisecond,
		Timeout:      5 * time.Second,
	}

	go func() {
		var jobName string
		require.Eventually(t, func() bool {
			jobs, err := clientset.BatchV1().Jobs("default").List(context.Background(), metav1.ListOptions{})
			if err != nil || len(jobs.Items) == 0 {
				return false
			}
			jobName = jobs.Items[0].Name
			return true
		}, 2*time.Second, 5*time.Millisecond, "job was never created")

		_, err := clientset.CoreV1().Pods("default").Create(context.Background(), &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: jobName + "-pod", Namespace: "default", Labels: map[string]string{"job-name": jobName}},
			Status: corev1.PodStatus{
				ContainerStatuses: []corev1.ContainerStatus{{
					Name:  "step",
					State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: 7}},
				}},
			},
		}, metav1.CreateOptions{})
		require.NoError(t, err)

		job, err := clientset.BatchV1().Jobs("default").Get(context.Background(), jobName, metav1.GetOptions{})
		require.NoError(t, err)
		job.Status.Failed = 1
		_, err = clientset.BatchV1().Jobs("default").UpdateStatus(context.Background(), job, metav1.UpdateOptions{})
		require.NoError(t, err)
	}()

	_, _, exitCode, err := r.RunStep(context.Background(), WorkflowStep{
		Name:      "step",
		Container: "my-image:latest",
		Script:    "exit 7",
	}, nil, "", nil)

	require.Error(t, err)
	assert.Equal(t, 7, exitCode)
}

func TestKubernetesJobStepRunner_TimesOutIfJobNeverCompletes(t *testing.T) {
	r := &KubernetesJobStepRunner{
		Client:       k8sfake.NewClientset(),
		Namespace:    "default",
		PollInterval: 5 * time.Millisecond,
		Timeout:      30 * time.Millisecond,
	}
	_, _, _, err := r.RunStep(context.Background(), WorkflowStep{
		Name:      "step",
		Container: "my-image:latest",
		Script:    "sleep 999",
	}, nil, "", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "timed out")
}

func TestSanitizeJobName(t *testing.T) {
	assert.Regexp(t, `^hyve-my-step-\d+$`, sanitizeJobName("My Step!"))
	assert.Regexp(t, `^hyve-step-\d+$`, sanitizeJobName(""))

	// Job names double as label values via the Job controller's own
	// job-name label — must stay short and DNS-1123-safe regardless of an
	// unusually long step name.
	long := ""
	for i := 0; i < 100; i++ {
		long += "x"
	}
	name := sanitizeJobName(long)
	assert.LessOrEqual(t, len(name), 63)
}
