package shell

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
	"sigs.k8s.io/yaml"
)

type ident struct{}

func (ident) KubeUser() string     { return "hyve:alice" }
func (ident) KubeGroups() []string { return []string{"hyve:devs", "system:authenticated"} }

// runPods marks every shell pod Running, as a kubelet would.
func runPods(ctx context.Context, cs *fake.Clientset) {
	for ctx.Err() == nil {
		if l, err := cs.CoreV1().Pods(Namespace).List(ctx, metav1.ListOptions{}); err == nil {
			for _, p := range l.Items {
				if p.Status.Phase == "" {
					p.Status.Phase = corev1.PodRunning
					_, _ = cs.CoreV1().Pods(Namespace).UpdateStatus(ctx, &p, metav1.UpdateOptions{})
				}
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestStartAndStop(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cs := fake.NewClientset()
	go runPods(ctx, cs)

	sh, err := (&Manager{Image: "example/shell:1"}).Start(ctx, cs, "dev.web", ident{}, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cs.CoreV1().Namespaces().Get(ctx, Namespace, metav1.GetOptions{}); err != nil {
		t.Fatalf("namespace: %v", err)
	}
	role, err := cs.RbacV1().ClusterRoles().Get(ctx, roleName(sh.ID), metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	// Only this user and these groups.
	want := []rbacv1.PolicyRule{
		{APIGroups: []string{""}, Resources: []string{"users"}, Verbs: []string{"impersonate"}, ResourceNames: []string{"hyve:alice"}},
		{APIGroups: []string{""}, Resources: []string{"groups"}, Verbs: []string{"impersonate"}, ResourceNames: []string{"hyve:devs", "system:authenticated"}},
	}
	if len(role.Rules) != 2 || !slices.Equal(role.Rules[0].ResourceNames, want[0].ResourceNames) || !slices.Equal(role.Rules[1].ResourceNames, want[1].ResourceNames) {
		t.Errorf("role rules = %+v", role.Rules)
	}
	b, err := cs.RbacV1().ClusterRoleBindings().Get(ctx, roleName(sh.ID), metav1.GetOptions{})
	if err != nil || b.Subjects[0].Name != objectName(sh.ID) || b.Subjects[0].Namespace != Namespace {
		t.Errorf("binding = %+v, %v", b, err)
	}
	pod, err := cs.CoreV1().Pods(Namespace).Get(ctx, sh.Pod, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if pod.Spec.ServiceAccountName != objectName(sh.ID) || pod.Spec.Containers[0].Image != "example/shell:1" || *pod.Spec.ActiveDeadlineSeconds != 3600 {
		t.Errorf("pod spec = %+v", pod.Spec)
	}
	if owner, err := Owner(ctx, cs, sh.ID); err != nil || owner != "alice" {
		t.Errorf("Owner = %q, %v", owner, err)
	}

	if err := Stop(ctx, cs, sh.ID); err != nil {
		t.Fatal(err)
	}
	if l, _ := cs.CoreV1().Pods(Namespace).List(ctx, metav1.ListOptions{}); len(l.Items) != 0 {
		t.Errorf("pods left: %d", len(l.Items))
	}
	if l, _ := cs.RbacV1().ClusterRoles().List(ctx, metav1.ListOptions{}); len(l.Items) != 0 {
		t.Errorf("roles left: %d", len(l.Items))
	}
	if l, _ := cs.CoreV1().ServiceAccounts(Namespace).List(ctx, metav1.ListOptions{}); len(l.Items) != 0 {
		t.Errorf("service accounts left: %d", len(l.Items))
	}
}

func TestStartCleansUpWhenThePodCantRun(t *testing.T) {
	cs := fake.NewClientset()
	_, err := (&Manager{StartTimeout: 200 * time.Millisecond}).Start(context.Background(), cs, "local", ident{}, "alice")
	if err == nil || !strings.Contains(err.Error(), "didn't start") {
		t.Fatalf("err = %v", err)
	}
	if l, _ := cs.RbacV1().ClusterRoleBindings().List(context.Background(), metav1.ListOptions{}); len(l.Items) != 0 {
		t.Errorf("bindings left: %d", len(l.Items))
	}
	if l, _ := cs.CoreV1().ConfigMaps(Namespace).List(context.Background(), metav1.ListOptions{}); len(l.Items) != 0 {
		t.Errorf("config maps left: %d", len(l.Items))
	}
}

func TestSweep(t *testing.T) {
	ctx := context.Background()
	old := metav1.NewTime(time.Now().Add(-2 * time.Hour))
	meta := func(name, id string, ns string) metav1.ObjectMeta {
		return metav1.ObjectMeta{Name: name, Namespace: ns, Labels: map[string]string{LabelShell: id}, CreationTimestamp: old}
	}
	cs := fake.NewClientset(
		// Over: its deadline passed.
		&corev1.Pod{ObjectMeta: meta("shell-done", "done", Namespace), Status: corev1.PodStatus{Phase: corev1.PodFailed}},
		&rbacv1.ClusterRole{ObjectMeta: meta("hyve-shell-done", "done", "")},
		// Half-made long ago: no pod.
		&corev1.ServiceAccount{ObjectMeta: meta("shell-orphan", "orphan", Namespace)},
		&rbacv1.ClusterRoleBinding{ObjectMeta: meta("hyve-shell-orphan", "orphan", "")},
		// Being made right now: left alone.
		&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "shell-new", Namespace: Namespace, Labels: map[string]string{LabelShell: "new"}, CreationTimestamp: metav1.Now()}},
	)
	sweep(ctx, cs)
	if l, _ := cs.CoreV1().Pods(Namespace).List(ctx, metav1.ListOptions{}); len(l.Items) != 0 {
		t.Errorf("finished pod not swept")
	}
	if l, _ := cs.RbacV1().ClusterRoles().List(ctx, metav1.ListOptions{}); len(l.Items) != 0 {
		t.Errorf("finished shell's role not swept")
	}
	if l, _ := cs.RbacV1().ClusterRoleBindings().List(ctx, metav1.ListOptions{}); len(l.Items) != 0 {
		t.Errorf("orphaned binding not swept")
	}
	if l, _ := cs.CoreV1().ServiceAccounts(Namespace).List(ctx, metav1.ListOptions{}); len(l.Items) != 0 {
		t.Errorf("orphaned service account not swept")
	}
	if l, _ := cs.CoreV1().ConfigMaps(Namespace).List(ctx, metav1.ListOptions{}); len(l.Items) != 1 {
		t.Errorf("a shell being made was swept")
	}
}

func TestKubeconfig(t *testing.T) {
	b, err := Kubeconfig("dev.web", ident{})
	if err != nil {
		t.Fatal(err)
	}
	var kc struct {
		Users []struct {
			User map[string]any `json:"user"`
		} `json:"users"`
		Clusters []struct {
			Cluster map[string]any `json:"cluster"`
		} `json:"clusters"`
		CurrentContext string `json:"current-context"`
	}
	if err := yaml.Unmarshal(b, &kc); err != nil {
		t.Fatal(err)
	}
	u := kc.Users[0].User
	if u["as"] != "hyve:alice" || u["tokenFile"] != "/var/run/secrets/kubernetes.io/serviceaccount/token" || len(u["as-groups"].([]any)) != 2 {
		t.Errorf("user = %v", u)
	}
	if kc.Clusters[0].Cluster["server"] != "https://kubernetes.default.svc" || kc.CurrentContext != "dev.web" {
		t.Errorf("kubeconfig = %s", b)
	}
	// No credential of hyve's in it.
	if strings.Contains(string(b), "hs-") || strings.Contains(string(b), "token:") {
		t.Errorf("kubeconfig carries a token: %s", b)
	}
}
