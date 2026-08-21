package k8sjob

import (
	"bytes"
	"context"
	"log"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sfake "k8s.io/client-go/kubernetes/fake"
)

// TestRun_EmptyImageFallsBackToDefault confirms an unset RunRequest.Image
// no longer hard-fails — it falls back to defaultFallbackImage instead.
func TestRun_EmptyImageFallsBackToDefault(t *testing.T) {
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
		assert.Equal(t, defaultFallbackImage, job.Spec.Template.Spec.Containers[0].Image)

		job.Status.Succeeded = 1
		_, err = clientset.BatchV1().Jobs("default").UpdateStatus(context.Background(), job, metav1.UpdateOptions{})
		require.NoError(t, err)
	}()

	_, exitCode, err := Run(context.Background(), clientset, RunRequest{
		Name:         "run",
		Namespace:    "default",
		Script:       "echo hi",
		PollInterval: 10 * time.Millisecond,
		Timeout:      5 * time.Second,
	}, nil)
	<-done
	require.NoError(t, err)
	assert.Equal(t, 0, exitCode)
}

// TestRun_ImageInstalls_MatchingEntryPrefixesScript confirms an
// ImageInstalls entry matching the resolved image (here, the fallback
// image, proving the two features compose) prefixes its Install script
// onto the Job's command.
func TestRun_ImageInstalls_MatchingEntryPrefixesScript(t *testing.T) {
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
		cmd := job.Spec.Template.Spec.Containers[0].Command
		require.Len(t, cmd, 3)
		assert.Contains(t, cmd[2], "apt-get install -y jq")
		assert.Contains(t, cmd[2], "echo hi") // the original script, still present after the install block

		job.Status.Succeeded = 1
		_, err = clientset.BatchV1().Jobs("default").UpdateStatus(context.Background(), job, metav1.UpdateOptions{})
		require.NoError(t, err)
	}()

	_, exitCode, err := Run(context.Background(), clientset, RunRequest{
		Name:      "run",
		Namespace: "default",
		Script:    "echo hi",
		ImageInstalls: []ImageInstall{
			{Image: "other:image", Install: "should not appear"},
			{Image: defaultFallbackImage, Install: "apt-get install -y jq"},
		},
		PollInterval: 10 * time.Millisecond,
		Timeout:      5 * time.Second,
	}, nil)
	<-done
	require.NoError(t, err)
	assert.Equal(t, 0, exitCode)
}

// TestRun_ImageInstalls_NoMatchLeavesScriptUnchanged confirms an
// ImageInstalls list with no entry matching the resolved image is a no-op.
func TestRun_ImageInstalls_NoMatchLeavesScriptUnchanged(t *testing.T) {
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
		assert.Equal(t, []string{"/bin/sh", "-c", "echo hi"}, job.Spec.Template.Spec.Containers[0].Command)

		job.Status.Succeeded = 1
		_, err = clientset.BatchV1().Jobs("default").UpdateStatus(context.Background(), job, metav1.UpdateOptions{})
		require.NoError(t, err)
	}()

	_, exitCode, err := Run(context.Background(), clientset, RunRequest{
		Name:          "run",
		Namespace:     "default",
		Image:         "my-image:latest",
		Script:        "echo hi",
		ImageInstalls: []ImageInstall{{Image: "some-other-image:latest", Install: "apt-get install -y jq"}},
		PollInterval:  10 * time.Millisecond,
		Timeout:       5 * time.Second,
	}, nil)
	<-done
	require.NoError(t, err)
	assert.Equal(t, 0, exitCode)
}

// TestRun_LogsOnlyWhenImageFallsBack confirms the fallback log line fires
// only when RunRequest.Image is empty, never when explicitly set — the
// only durable trace a fallback fired, since the Job itself is deleted
// within ~30s regardless of outcome.
func TestRun_LogsOnlyWhenImageFallsBack(t *testing.T) {
	var buf bytes.Buffer
	origOutput := log.Writer()
	origFlags := log.Flags()
	log.SetOutput(&buf)
	log.SetFlags(0)
	t.Cleanup(func() {
		log.SetOutput(origOutput)
		log.SetFlags(origFlags)
	})

	clientset := k8sfake.NewClientset()
	go func() {
		require.Eventually(t, func() bool {
			jobs, err := clientset.BatchV1().Jobs("default").List(context.Background(), metav1.ListOptions{})
			if err != nil || len(jobs.Items) == 0 {
				return false
			}
			job := &jobs.Items[0]
			job.Status.Succeeded = 1
			_, err = clientset.BatchV1().Jobs("default").UpdateStatus(context.Background(), job, metav1.UpdateOptions{})
			return err == nil
		}, 2*time.Second, 5*time.Millisecond, "job was never created")
	}()
	_, _, err := Run(context.Background(), clientset, RunRequest{
		Name: "explicit-image-run", Namespace: "default", Image: "my-image:latest", Script: "echo hi",
		PollInterval: 10 * time.Millisecond, Timeout: 5 * time.Second,
	}, nil)
	require.NoError(t, err)
	assert.NotContains(t, buf.String(), "falling back", "no fallback log line expected when Image is explicitly set")

	buf.Reset()
	clientset2 := k8sfake.NewClientset()
	go func() {
		require.Eventually(t, func() bool {
			jobs, err := clientset2.BatchV1().Jobs("default").List(context.Background(), metav1.ListOptions{})
			if err != nil || len(jobs.Items) == 0 {
				return false
			}
			job := &jobs.Items[0]
			job.Status.Succeeded = 1
			_, err = clientset2.BatchV1().Jobs("default").UpdateStatus(context.Background(), job, metav1.UpdateOptions{})
			return err == nil
		}, 2*time.Second, 5*time.Millisecond, "job was never created")
	}()
	_, _, err = Run(context.Background(), clientset2, RunRequest{
		Name: "empty-image-run", Namespace: "default", Script: "echo hi",
		PollInterval: 10 * time.Millisecond, Timeout: 5 * time.Second,
	}, nil)
	require.NoError(t, err)
	assert.Contains(t, buf.String(), "falling back")
}

func TestRun_NoScriptIsAHardError(t *testing.T) {
	_, _, err := Run(context.Background(), k8sfake.NewClientset(), RunRequest{Name: "run", Namespace: "default", Image: "alpine:3"}, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no command or script")
}

func TestBuildLocalObjectRefs(t *testing.T) {
	assert.Nil(t, buildLocalObjectRefs(nil))
	assert.Nil(t, buildLocalObjectRefs([]string{}))
	assert.Equal(t,
		[]corev1.LocalObjectReference{{Name: "a"}, {Name: "b"}},
		buildLocalObjectRefs([]string{"a", "b"}),
	)
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
		assert.Equal(t, []corev1.LocalObjectReference{{Name: "ghcr-pull-secret"}}, job.Spec.Template.Spec.ImagePullSecrets)

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
		Name:             "run",
		Namespace:        "default",
		Image:            "my-image:latest",
		Script:           "echo hello",
		Env:              []string{"HYVE_CLUSTER_NAME=demo"},
		WorkingDir:       "/workdir",
		ImagePullSecrets: []string{"ghcr-pull-secret"},
		PollInterval:     10 * time.Millisecond,
		Timeout:          5 * time.Second,
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
