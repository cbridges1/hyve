// Package shell runs the UI's kubectl shell (HYVE-PROPOSAL.md, "Kubectl
// shell"): a short-lived pod on the cluster, from an image with kubectl,
// helm and k9s, whose terminal the browser reaches through the host.
//
// kubectl in the pod is the signed-in user. The pod runs as its own
// ServiceAccount, which may only impersonate that user and the user's
// groups, and its kubeconfig talks to the cluster's own API server with
// that ServiceAccount's token and the impersonation. So the cluster's RBAC
// decides every command, as it does through the host's proxy, nothing has
// to reach the host from the pod (push clusters work the same), and no
// hyve session is stored on the cluster: the token is the pod's, and stops
// working when the pod is deleted.
//
// The host makes these objects with its own access to the cluster (the
// agent's service account, a push cluster's CAPI kubeconfig, or its own on
// the management cluster), in the hyve-shell namespace, and deletes them
// when the terminal closes. A shell the host never got to clean up ends at
// the pod's deadline, and the next shell on that cluster sweeps it away.
package shell

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/yaml"
)

const (
	// Namespace holds every shell's objects, on every cluster.
	Namespace = "hyve-shell"
	// LabelShell is on every object a shell makes: its ID.
	LabelShell = "hyve.io/shell"
	// LabelUser is the hyve user a shell belongs to.
	LabelUser = "hyve.io/shell-user"
	// Container is the shell's container name.
	Container = "shell"
	// DefaultImage is the image the shell runs: Rancher's, with kubectl,
	// helm, k9s and kustomize.
	DefaultImage = "rancher/shell:v0.8.2"
	// MaxDuration is how long a shell can last.
	MaxDuration = time.Hour
	// sweepGrace keeps the sweep away from a shell still being made.
	sweepGrace = 10 * time.Minute
)

// Identity is the user a shell's kubectl runs as.
type Identity interface {
	KubeUser() string
	KubeGroups() []string
}

// Shell is a started shell.
type Shell struct {
	ID        string    `json:"id"`
	Cluster   string    `json:"cluster"`
	Pod       string    `json:"pod"`
	ExpiresAt time.Time `json:"expiresAt"`
}

// Manager starts and stops shells.
type Manager struct {
	// Image overrides DefaultImage.
	Image string
	// StartTimeout bounds waiting for the pod to run (pulling the image
	// on a new node takes a while); zero means two minutes.
	StartTimeout time.Duration
}

func (m *Manager) image() string {
	if m.Image != "" {
		return m.Image
	}
	return DefaultImage
}

func objectName(id string) string { return "shell-" + id }

// roleName is cluster-scoped, so it says whose it is.
func roleName(id string) string { return "hyve-shell-" + id }

func newID() (string, error) {
	b := make([]byte, 5)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// Start makes a shell on the cluster cs reaches, for ident, and waits for
// its pod to run. cluster is the cluster's ID, for the kubeconfig's names.
func (m *Manager) Start(ctx context.Context, cs kubernetes.Interface, cluster string, ident Identity, username string) (*Shell, error) {
	id, err := newID()
	if err != nil {
		return nil, err
	}
	if err := ensureNamespace(ctx, cs); err != nil {
		return nil, err
	}
	sweep(ctx, cs)

	// Usernames are DNS labels (signin.ValidUsername), so fit a label.
	labels := map[string]string{LabelShell: id, LabelUser: username}
	meta := func(name, ns string) metav1.ObjectMeta {
		return metav1.ObjectMeta{Name: name, Namespace: ns, Labels: labels}
	}
	name := objectName(id)
	ok := false
	defer func() {
		if !ok {
			stopCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
			defer cancel()
			_ = Stop(stopCtx, cs, id)
		}
	}()

	if _, err := cs.CoreV1().ServiceAccounts(Namespace).Create(ctx, &corev1.ServiceAccount{ObjectMeta: meta(name, Namespace)}, metav1.CreateOptions{}); err != nil {
		return nil, fmt.Errorf("creating the shell's service account: %w", err)
	}
	// Only this user and these groups: the ServiceAccount is no one else.
	if _, err := cs.RbacV1().ClusterRoles().Create(ctx, &rbacv1.ClusterRole{
		ObjectMeta: meta(roleName(id), ""),
		Rules: []rbacv1.PolicyRule{
			{APIGroups: []string{""}, Resources: []string{"users"}, Verbs: []string{"impersonate"}, ResourceNames: []string{ident.KubeUser()}},
			{APIGroups: []string{""}, Resources: []string{"groups"}, Verbs: []string{"impersonate"}, ResourceNames: ident.KubeGroups()},
		},
	}, metav1.CreateOptions{}); err != nil {
		return nil, fmt.Errorf("creating the shell's role: %w", err)
	}
	if _, err := cs.RbacV1().ClusterRoleBindings().Create(ctx, &rbacv1.ClusterRoleBinding{
		ObjectMeta: meta(roleName(id), ""),
		RoleRef:    rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "ClusterRole", Name: roleName(id)},
		Subjects:   []rbacv1.Subject{{Kind: rbacv1.ServiceAccountKind, Name: name, Namespace: Namespace}},
	}, metav1.CreateOptions{}); err != nil {
		return nil, fmt.Errorf("binding the shell's role: %w", err)
	}
	kc, err := Kubeconfig(cluster, ident)
	if err != nil {
		return nil, err
	}
	if _, err := cs.CoreV1().ConfigMaps(Namespace).Create(ctx, &corev1.ConfigMap{
		ObjectMeta: meta(name, Namespace),
		Data:       map[string]string{"config": string(kc)},
	}, metav1.CreateOptions{}); err != nil {
		return nil, fmt.Errorf("creating the shell's kubeconfig: %w", err)
	}
	pod, err := cs.CoreV1().Pods(Namespace).Create(ctx, m.pod(id, labels), metav1.CreateOptions{})
	if err != nil {
		return nil, fmt.Errorf("creating the shell's pod: %w", err)
	}
	if err := m.waitRunning(ctx, cs, pod.Name); err != nil {
		return nil, err
	}
	ok = true
	return &Shell{ID: id, Cluster: cluster, Pod: pod.Name, ExpiresAt: pod.CreationTimestamp.Add(MaxDuration).UTC()}, nil
}

func (m *Manager) pod(id string, labels map[string]string) *corev1.Pod {
	deadline := int64(MaxDuration / time.Second)
	grace := int64(0)
	automount := true
	f := false
	t := true
	uid := int64(1000)
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: objectName(id), Namespace: Namespace, Labels: labels},
		Spec: corev1.PodSpec{
			ServiceAccountName:            objectName(id),
			AutomountServiceAccountToken:  &automount,
			ActiveDeadlineSeconds:         &deadline,
			TerminationGracePeriodSeconds: &grace,
			RestartPolicy:                 corev1.RestartPolicyNever,
			NodeSelector:                  map[string]string{"kubernetes.io/os": "linux"},
			// Clusters with only control-plane nodes still get a shell.
			Tolerations: []corev1.Toleration{
				{Key: "node-role.kubernetes.io/control-plane", Operator: corev1.TolerationOpExists, Effect: corev1.TaintEffectNoSchedule},
				{Key: "node-role.kubernetes.io/master", Operator: corev1.TolerationOpExists, Effect: corev1.TaintEffectNoSchedule},
			},
			SecurityContext: &corev1.PodSecurityContext{RunAsNonRoot: &t, RunAsUser: &uid, RunAsGroup: &uid, FSGroup: &uid, SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault}},
			Containers: []corev1.Container{{
				Name:    Container,
				Image:   m.image(),
				Command: []string{"sleep", fmt.Sprint(deadline)},
				Env:     []corev1.EnvVar{{Name: "KUBECONFIG", Value: "/etc/hyve-shell/config"}},
				VolumeMounts: []corev1.VolumeMount{
					{Name: "kubeconfig", MountPath: "/etc/hyve-shell", ReadOnly: true},
				},
				SecurityContext: &corev1.SecurityContext{
					AllowPrivilegeEscalation: &f,
					Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
				},
				Resources: corev1.ResourceRequirements{
					Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("10m"), corev1.ResourceMemory: resource.MustParse("32Mi")},
					Limits:   corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("512Mi")},
				},
			}},
			Volumes: []corev1.Volume{{
				Name:         "kubeconfig",
				VolumeSource: corev1.VolumeSource{ConfigMap: &corev1.ConfigMapVolumeSource{LocalObjectReference: corev1.LocalObjectReference{Name: objectName(id)}}},
			}},
		},
	}
}

// waitRunning waits for the pod to run, or says why it can't.
func (m *Manager) waitRunning(ctx context.Context, cs kubernetes.Interface, name string) error {
	timeout := m.StartTimeout
	if timeout == 0 {
		timeout = 2 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	why := "it wasn't scheduled"
	for {
		p, err := cs.CoreV1().Pods(Namespace).Get(ctx, name, metav1.GetOptions{})
		if err == nil {
			switch p.Status.Phase {
			case corev1.PodRunning:
				return nil
			case corev1.PodFailed, corev1.PodSucceeded:
				return fmt.Errorf("the shell's pod stopped: %s", p.Status.Message)
			}
			if w := waiting(p); w != "" {
				why = w
			}
		} else if ctx.Err() == nil {
			return err
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("the shell's pod didn't start within %s: %s", timeout, why)
		case <-time.After(time.Second):
		}
	}
}

// waiting is why a pod's container hasn't started, or why it can't be
// scheduled.
func waiting(p *corev1.Pod) string {
	for _, c := range p.Status.ContainerStatuses {
		if w := c.State.Waiting; w != nil && w.Reason != "ContainerCreating" {
			return strings.TrimSpace(w.Reason + ": " + w.Message)
		}
	}
	for _, c := range p.Status.Conditions {
		if c.Type == corev1.PodScheduled && c.Status == corev1.ConditionFalse && c.Message != "" {
			return c.Message
		}
	}
	if len(p.Status.ContainerStatuses) > 0 {
		return "its image is still being pulled"
	}
	return ""
}

// Kubeconfig is the shell's kubeconfig: the cluster's API server from
// inside the cluster, the pod's ServiceAccount token, and the user to
// impersonate.
func Kubeconfig(cluster string, ident Identity) ([]byte, error) {
	const sa = "/var/run/secrets/kubernetes.io/serviceaccount"
	return yaml.Marshal(map[string]any{
		"apiVersion": "v1",
		"kind":       "Config",
		"clusters": []any{map[string]any{"name": cluster, "cluster": map[string]any{
			"server":                "https://kubernetes.default.svc",
			"certificate-authority": sa + "/ca.crt",
		}}},
		"users": []any{map[string]any{"name": ident.KubeUser(), "user": map[string]any{
			"tokenFile": sa + "/token",
			"as":        ident.KubeUser(),
			"as-groups": ident.KubeGroups(),
		}}},
		"contexts": []any{map[string]any{"name": cluster, "context": map[string]any{
			"cluster": cluster, "user": ident.KubeUser(), "namespace": "default",
		}}},
		"current-context": cluster,
	})
}

func ensureNamespace(ctx context.Context, cs kubernetes.Interface) error {
	_, err := cs.CoreV1().Namespaces().Get(ctx, Namespace, metav1.GetOptions{})
	if err == nil {
		return nil
	}
	if !apierrors.IsNotFound(err) {
		return fmt.Errorf("looking for namespace %s: %w", Namespace, err)
	}
	_, err = cs.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{
		Name: Namespace, Labels: map[string]string{"app.kubernetes.io/managed-by": "hyve"},
	}}, metav1.CreateOptions{})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("creating namespace %s: %w", Namespace, err)
	}
	return nil
}

// Owner returns the hyve user a running shell belongs to.
func Owner(ctx context.Context, cs kubernetes.Interface, id string) (string, error) {
	p, err := cs.CoreV1().Pods(Namespace).Get(ctx, objectName(id), metav1.GetOptions{})
	if err != nil {
		return "", err
	}
	if p.Labels[LabelShell] != id {
		return "", apierrors.NewNotFound(corev1.Resource("pods"), objectName(id))
	}
	return p.Labels[LabelUser], nil
}

// Stop deletes a shell's objects.
func Stop(ctx context.Context, cs kubernetes.Interface, id string) error {
	now := int64(0)
	opts := metav1.DeleteOptions{GracePeriodSeconds: &now}
	name := objectName(id)
	var errs []error
	add := func(err error) {
		if err != nil && !apierrors.IsNotFound(err) {
			errs = append(errs, err)
		}
	}
	add(cs.CoreV1().Pods(Namespace).Delete(ctx, name, opts))
	add(cs.RbacV1().ClusterRoleBindings().Delete(ctx, roleName(id), opts))
	add(cs.RbacV1().ClusterRoles().Delete(ctx, roleName(id), opts))
	add(cs.CoreV1().ConfigMaps(Namespace).Delete(ctx, name, opts))
	add(cs.CoreV1().ServiceAccounts(Namespace).Delete(ctx, name, opts))
	return errors.Join(errs...)
}

// sweep stops shells that are over: their pod finished (its deadline
// passed) or is gone, and whatever a shell left half-made. Best effort.
func sweep(ctx context.Context, cs kubernetes.Interface) {
	sel := metav1.ListOptions{LabelSelector: LabelShell}
	live := map[string]bool{}
	pods, err := cs.CoreV1().Pods(Namespace).List(ctx, sel)
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-MaxDuration - sweepGrace)
	for _, p := range pods.Items {
		id := p.Labels[LabelShell]
		if p.Status.Phase == corev1.PodSucceeded || p.Status.Phase == corev1.PodFailed || p.CreationTimestamp.Time.Before(cutoff) {
			_ = Stop(ctx, cs, id)
			continue
		}
		live[id] = true
	}
	stale := map[string]bool{}
	old := func(m metav1.ObjectMeta) {
		if id := m.Labels[LabelShell]; id != "" && !live[id] && time.Since(m.CreationTimestamp.Time) > sweepGrace {
			stale[id] = true
		}
	}
	if l, err := cs.CoreV1().ServiceAccounts(Namespace).List(ctx, sel); err == nil {
		for _, o := range l.Items {
			old(o.ObjectMeta)
		}
	}
	if l, err := cs.CoreV1().ConfigMaps(Namespace).List(ctx, sel); err == nil {
		for _, o := range l.Items {
			old(o.ObjectMeta)
		}
	}
	if l, err := cs.RbacV1().ClusterRoles().List(ctx, sel); err == nil {
		for _, o := range l.Items {
			old(o.ObjectMeta)
		}
	}
	if l, err := cs.RbacV1().ClusterRoleBindings().List(ctx, sel); err == nil {
		for _, o := range l.Items {
			old(o.ObjectMeta)
		}
	}
	for id := range stale {
		_ = Stop(ctx, cs, id)
	}
}

// ExecPath is the API path of a shell's terminal: welcome (the image's
// greeting, then bash) on a TTY.
func ExecPath(id string) string {
	return "/api/v1/namespaces/" + Namespace + "/pods/" + objectName(id) +
		"/exec?container=" + Container + "&command=welcome&stdin=true&stdout=true&tty=true"
}
