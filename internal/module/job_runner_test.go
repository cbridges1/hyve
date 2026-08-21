package module

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sfake "k8s.io/client-go/kubernetes/fake"

	"github.com/cbridges1/hyve/internal/k8sjob"
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

// TestExecutor_ExecuteYAMLWorkflowOperation_DispatchesToJobWhenRunnerSet
// confirms a module operation expressed as a kind:Workflow YAML file (e.g.
// status.yaml, as real modules like hyve-civo-module use for every
// operation) dispatches to e.Runner in cluster mode instead of always
// running inline — a gap that previously meant such modules never went
// through Job dispatch at all, regardless of Runner/Image being set.
func TestExecutor_ExecuteYAMLWorkflowOperation_DispatchesToJobWhenRunnerSet(t *testing.T) {
	clientset := k8sfake.NewClientset()
	runner := &JobRunner{Client: clientset, Namespace: "default", PollInterval: 10 * time.Millisecond, Timeout: 5 * time.Second}

	moduleDir := t.TempDir()
	statusYAML := `apiVersion: v1
kind: Workflow
metadata:
  name: status
spec:
  jobs:
    main:
      steps:
        - run: echo HYVE_CLUSTER_STATUS=ACTIVE
`
	require.NoError(t, os.WriteFile(filepath.Join(moduleDir, "status.yaml"), []byte(statusYAML), 0644))

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
		assert.Equal(t, "my-driver:latest", job.Spec.Template.Spec.Containers[0].Image)
		assert.Contains(t, job.Spec.Template.Spec.Containers[0].Command[2], "echo HYVE_CLUSTER_STATUS=ACTIVE")

		job.Status.Succeeded = 1
		_, err = clientset.BatchV1().Jobs("default").UpdateStatus(context.Background(), job, metav1.UpdateOptions{})
		require.NoError(t, err)
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

// TestJobRunner_ImageInstalls_ThreadedThroughToJob confirms JobRunner.Run
// passes its ImageInstalls through into the k8sjob.RunRequest it builds —
// set once at startup from HyveConfig.spec.imageInstalls (cmd/controller/
// run.go), reaching every module Job the same way ImagePullSecrets does.
func TestJobRunner_ImageInstalls_ThreadedThroughToJob(t *testing.T) {
	clientset := k8sfake.NewClientset()
	runner := &JobRunner{
		Client: clientset, Namespace: "default",
		PollInterval:  10 * time.Millisecond,
		Timeout:       5 * time.Second,
		ImageInstalls: []k8sjob.ImageInstall{{Image: "my-driver:latest", Install: "apt-get install -y jq"}},
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

	_, exitCode, err := runner.Run(context.Background(), "run", "my-driver:latest", "echo hi", nil)
	<-done
	require.NoError(t, err)
	assert.Equal(t, 0, exitCode)
}

// TestExecutor_ExecuteAuth_DispatchesToJobAndWrapsScriptUnmodified confirms
// executeAuth's Job-dispatch branch sends a *wrapped* version of the auth
// script (setting $KUBECONFIG to an in-container temp path and relaying it
// back over stdout between sentinel markers — see runAuthScript) while the
// module author's own script text appears verbatim, untouched, inside that
// wrapper. The fake clientset's GetLogs never returns real content (same
// limitation noted in internal/k8sjob's and internal/workflow's own
// tests), so the actual relay-and-write path — already exercised
// end-to-end by TestExecuteAuth_WritesPerClusterKubeconfig_NotProcessEnv via
// the inline (Runner == nil) path, since runAuthScript's post-wrapper write
// logic is identical either way — is confirmed for real against a live k3d
// cluster instead.
func TestExecutor_ExecuteAuth_DispatchesToJobAndWrapsScriptUnmodified(t *testing.T) {
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
        script: "civo kubernetes config \"$HYVE_CLUSTER_NAME\" --save --yes"
      exports: KUBECONFIG
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
		wrapped := job.Spec.Template.Spec.Containers[0].Command[2]
		assert.Contains(t, wrapped, "civo kubernetes config", "the module's own script must appear verbatim inside the wrapper")
		assert.Contains(t, wrapped, "export KUBECONFIG=/tmp/hyve-auth-kubeconfig")
		assert.Contains(t, wrapped, kubeconfigBeginMarker)
		assert.Contains(t, wrapped, kubeconfigEndMarker)

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
	assert.Empty(t, result.Outputs["KUBECONFIG"], "fake clientset serves no real pod logs, so nothing is relayed back here")

	jobs, err := clientset.BatchV1().Jobs("default").List(context.Background(), metav1.ListOptions{})
	require.NoError(t, err)
	assert.Empty(t, jobs.Items, "Job should be cleaned up after Execute returns")
}
