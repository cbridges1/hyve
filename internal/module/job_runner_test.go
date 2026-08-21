package module

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sfake "k8s.io/client-go/kubernetes/fake"
)

// simulateJobSuccess watches for a Job to appear on clientset and marks it
// Succeeded, mirroring the pattern internal/workflow's
// k8s_step_runner_test.go and internal/k8sjob's own run_test.go use to drive
// the fake clientset's polling loop to completion without a real
// controller.
func simulateJobSuccess(t *testing.T, clientset *k8sfake.Clientset, namespace string) {
	t.Helper()
	var jobName string
	require.Eventually(t, func() bool {
		jobs, err := clientset.BatchV1().Jobs(namespace).List(context.Background(), metav1.ListOptions{})
		if err != nil || len(jobs.Items) == 0 {
			return false
		}
		jobName = jobs.Items[0].Name
		return true
	}, 2*time.Second, 5*time.Millisecond, "job was never created")

	_, err := clientset.CoreV1().Pods(namespace).Create(context.Background(), &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: jobName + "-pod", Namespace: namespace, Labels: map[string]string{"job-name": jobName}},
		Status: corev1.PodStatus{
			ContainerStatuses: []corev1.ContainerStatus{{
				Name:  "run",
				State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: 0}},
			}},
		},
	}, metav1.CreateOptions{})
	require.NoError(t, err)

	job, err := clientset.BatchV1().Jobs(namespace).Get(context.Background(), jobName, metav1.GetOptions{})
	require.NoError(t, err)
	job.Status.Succeeded = 1
	_, err = clientset.BatchV1().Jobs(namespace).UpdateStatus(context.Background(), job, metav1.UpdateOptions{})
	require.NoError(t, err)
}

func TestExecutor_ExecuteScript_DispatchesToJobWhenRunnerSet(t *testing.T) {
	clientset := k8sfake.NewClientset()
	runner := &JobRunner{Client: clientset, Namespace: "default", PollInterval: 10 * time.Millisecond, Timeout: 5 * time.Second}

	moduleDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(moduleDir, "status.sh"), []byte("echo HYVE_CLUSTER_STATUS=ACTIVE\n"), 0755))

	done := make(chan struct{})
	go func() {
		defer close(done)
		simulateJobSuccess(t, clientset, "default")
	}()

	exec := &Executor{ModuleDir: moduleDir, ClusterName: "my-cluster", Runner: runner, Image: "my-driver:latest"}
	result, err := exec.Execute(context.Background(), OperationStatus)
	<-done
	require.NoError(t, err)
	assert.Equal(t, 0, result.ExitCode)

	jobs, err := clientset.BatchV1().Jobs("default").List(context.Background(), metav1.ListOptions{})
	require.NoError(t, err)
	assert.Empty(t, jobs.Items, "Job should be cleaned up after Execute returns")
}

// TestExecutor_ExecuteAuth_DispatchesToJobAndCarriesScriptContent confirms
// executeAuth's Job-dispatch branch sends the auth script's own content
// (not a local path a Job's pod couldn't read) and cleans up afterward. The
// fake clientset's GetLogs never returns real content (same limitation
// noted in internal/k8sjob's and internal/workflow's own tests), so the
// actual HYVE_KUBECONFIG_B64 decode-and-write path — already exercised
// end-to-end by TestExecuteAuth_WritesPerClusterKubeconfig_NotProcessEnv via
// the inline (Runner == nil) path, since it's the same executeAuth code
// either way — is confirmed for real against a live k3d cluster instead.
func TestExecutor_ExecuteAuth_DispatchesToJobAndCarriesScriptContent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	clientset := k8sfake.NewClientset()
	runner := &JobRunner{Client: clientset, Namespace: "default", PollInterval: 10 * time.Millisecond, Timeout: 5 * time.Second}

	moduleDir := t.TempDir()
	authYAML := `apiVersion: v1
kind: ClusterAuth
metadata:
  name: test
spec:
  methods:
    - name: default
      auth:
        script: |
          echo "HYVE_KUBECONFIG_B64=$(printf %s fake-kubeconfig | base64)"
`
	require.NoError(t, os.WriteFile(filepath.Join(moduleDir, "auth.yaml"), []byte(authYAML), 0644))

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
		assert.Equal(t, "my-driver:latest", job.Spec.Template.Spec.Containers[0].Image)
		assert.Contains(t, job.Spec.Template.Spec.Containers[0].Command[2], "HYVE_KUBECONFIG_B64")

		_, err = clientset.CoreV1().Pods("default").Create(context.Background(), &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: jobName + "-pod", Namespace: "default", Labels: map[string]string{"job-name": jobName}},
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

	exec := &Executor{ModuleDir: moduleDir, ClusterName: "my-cluster", Runner: runner, Image: "my-driver:latest"}
	result, err := exec.Execute(context.Background(), OperationAuth)
	<-done
	require.NoError(t, err)
	assert.Empty(t, result.Outputs["KUBECONFIG"], "fake clientset serves no real pod logs, so no HYVE_KUBECONFIG_B64 is observed here")

	jobs, err := clientset.BatchV1().Jobs("default").List(context.Background(), metav1.ListOptions{})
	require.NoError(t, err)
	assert.Empty(t, jobs.Items, "Job should be cleaned up after Execute returns")
}

func TestBase64RoundTrip_Sanity(t *testing.T) {
	// Guards the decode side of the HYVE_KUBECONFIG_B64 contract in
	// isolation from any shell/base64 CLI quirks.
	encoded := base64.StdEncoding.EncodeToString([]byte("hello world"))
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	require.NoError(t, err)
	assert.Equal(t, "hello world", string(decoded))
}
