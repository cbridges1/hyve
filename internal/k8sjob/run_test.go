package k8sjob

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sfake "k8s.io/client-go/kubernetes/fake"
)

func TestRun_NoImageIsAHardError(t *testing.T) {
	_, _, err := Run(context.Background(), k8sfake.NewClientset(), RunRequest{Name: "run", Namespace: "default", Script: "echo hi"}, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no container image")
}

func TestRun_NoScriptIsAHardError(t *testing.T) {
	_, _, err := Run(context.Background(), k8sfake.NewClientset(), RunRequest{Name: "run", Namespace: "default", Image: "alpine:3"}, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no command or script")
}

// TestRun_CreatesJobWithExpectedSpecAndSucceeds drives Run against a fake
// clientset with the poll interval set fast, and a background goroutine
// that simulates the Job controller: marks the Job Succeeded and creates a
// labeled Pod once the Job exists.
func TestRun_CreatesJobWithExpectedSpecAndSucceeds(t *testing.T) {
	clientset := k8sfake.NewClientset()

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
		c := job.Spec.Template.Spec.Containers[0]
		assert.Equal(t, "my-image:latest", c.Image)
		assert.Equal(t, []string{"/bin/sh", "-c", "echo hello"}, c.Command)
		assert.Contains(t, c.Env, corev1.EnvVar{Name: "HYVE_CLUSTER_NAME", Value: "demo"})
		require.NotNil(t, job.Spec.BackoffLimit)
		assert.Equal(t, int32(0), *job.Spec.BackoffLimit)

		_, err = clientset.CoreV1().Pods("default").Create(context.Background(), &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      jobName + "-pod",
				Namespace: "default",
				Labels:    map[string]string{"job-name": jobName},
			},
			Status: corev1.PodStatus{
				ContainerStatuses: []corev1.ContainerStatus{{
					Name:  "run",
					State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: 0}},
				}},
			},
		}, metav1.CreateOptions{})
		require.NoError(t, err)

		job.Status.Succeeded = 1
		_, err = clientset.BatchV1().Jobs("default").UpdateStatus(context.Background(), job, metav1.UpdateOptions{})
		require.NoError(t, err)
	}()

	var output bytes.Buffer
	stdout, exitCode, err := Run(context.Background(), clientset, RunRequest{
		Name:         "run",
		Namespace:    "default",
		Image:        "my-image:latest",
		Script:       "echo hello",
		Env:          []string{"HYVE_CLUSTER_NAME=demo"},
		WorkingDir:   "/workdir",
		PollInterval: 10 * time.Millisecond,
		Timeout:      5 * time.Second,
	}, &output)

	<-done
	require.NoError(t, err)
	assert.Equal(t, 0, exitCode)
	_ = stdout // fake clientset's GetLogs has no real content to serve

	jobs, err := clientset.BatchV1().Jobs("default").List(context.Background(), metav1.ListOptions{})
	require.NoError(t, err)
	assert.Empty(t, jobs.Items, "Job should be cleaned up after Run returns")
}

func TestRun_JobFailureIsReportedAsAFailure(t *testing.T) {
	clientset := k8sfake.NewClientset()

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
					Name:  "run",
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

	_, exitCode, err := Run(context.Background(), clientset, RunRequest{
		Name:         "run",
		Namespace:    "default",
		Image:        "my-image:latest",
		Script:       "exit 7",
		PollInterval: 10 * time.Millisecond,
		Timeout:      5 * time.Second,
	}, nil)

	require.Error(t, err)
	assert.Equal(t, 7, exitCode)
}

func TestRun_TimesOutIfJobNeverCompletes(t *testing.T) {
	_, _, err := Run(context.Background(), k8sfake.NewClientset(), RunRequest{
		Name:         "run",
		Namespace:    "default",
		Image:        "my-image:latest",
		Script:       "sleep 999",
		PollInterval: 5 * time.Millisecond,
		Timeout:      30 * time.Millisecond,
	}, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "timed out")
}

func TestSanitizeJobName(t *testing.T) {
	assert.Regexp(t, `^hyve-my-run-\d+$`, sanitizeJobName("My Run!"))
	assert.Regexp(t, `^hyve-run-\d+$`, sanitizeJobName(""))

	long := ""
	for i := 0; i < 100; i++ {
		long += "x"
	}
	name := sanitizeJobName(long)
	assert.LessOrEqual(t, len(name), 63)
}
