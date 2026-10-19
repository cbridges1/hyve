package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/cbridges1/hyve/internal/clusters"
	"github.com/cbridges1/hyve/internal/login"
)

// With Rancher, `hyve access` makes Rancher's own bindings, what Rancher's
// Members tabs do: a ClusterRoleTemplateBinding on the cluster Turtles
// imported, or a ProjectRoleTemplateBinding with the hyve Environment
// Member role on the environment's Project.

var (
	crtbGVR        = schema.GroupVersionResource{Group: "management.cattle.io", Version: "v3", Resource: "clusterroletemplatebindings"}
	prtbGVR        = schema.GroupVersionResource{Group: "management.cattle.io", Version: "v3", Resource: "projectroletemplatebindings"}
	rancherUserGVR = schema.GroupVersionResource{Group: "management.cattle.io", Version: "v3", Resource: "users"}
	projectGVR     = schema.GroupVersionResource{Group: "management.cattle.io", Version: "v3", Resource: "projects"}
)

func (s *session) rancher() bool { return s.server != nil && s.server.Kind == login.KindRancher }

// rancherUserID turns a username (or a user ID) into a Rancher user ID.
func (s *session) rancherUserID(ctx context.Context, name string) (string, error) {
	if strings.HasPrefix(name, "u-") || strings.HasPrefix(name, "user-") {
		return name, nil
	}
	list, err := s.kube.Dynamic.Resource(rancherUserGVR).List(ctx, metav1.ListOptions{})
	if err != nil {
		return "", fmt.Errorf("looking up Rancher user %q (give the user ID, u-..., if you can't list users): %w", name, err)
	}
	for _, u := range list.Items {
		if un, _ := u.Object["username"].(string); un == name {
			return u.GetName(), nil
		}
	}
	return "", fmt.Errorf("no Rancher user %q", name)
}

func rancherGrant(ctx context.Context, s *session, user, group, role, cluster string, envMember bool) error {
	subject := map[string]any{}
	who := ""
	if user != "" {
		id, err := s.rancherUserID(ctx, user)
		if err != nil {
			return err
		}
		subject["userName"], who = id, "user "+user
	} else {
		subject["groupPrincipalName"], who = group, "group "+group
	}
	if envMember {
		ns, err := s.kube.Dynamic.Resource(namespaceGVR).Get(ctx, s.env(), metav1.GetOptions{})
		if err != nil {
			return err
		}
		project := ns.GetAnnotations()["hyve.io/project"]
		if project == "" {
			if pid := ns.GetAnnotations()["field.cattle.io/projectId"]; pid != "" {
				_, project, _ = strings.Cut(pid, ":")
			}
		}
		if project == "" {
			return fmt.Errorf("environment %s isn't in a Rancher Project yet (hyve-controller puts it in one)", s.env())
		}
		// Project bindings live in the project's backing namespace
		// (Rancher 2.9+), "local-p-..." for a project on the local cluster.
		bindingNS := "local-" + project
		if p, err := s.kube.Dynamic.Resource(projectGVR).Namespace("local").Get(ctx, project, metav1.GetOptions{}); err == nil {
			if bn, _, _ := unstructured.NestedString(p.Object, "status", "backingNamespace"); bn != "" {
				bindingNS = bn
			}
		}
		obj := &unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "management.cattle.io/v3", "kind": "ProjectRoleTemplateBinding",
			"metadata":         map[string]any{"generateName": "hyve-", "namespace": bindingNS},
			"projectName":      "local:" + project,
			"roleTemplateName": "hyve-environment-member",
		}}
		for k, v := range subject {
			obj.Object[k] = v
		}
		created, err := s.kube.Dynamic.Resource(prtbGVR).Namespace(bindingNS).Create(ctx, obj, metav1.CreateOptions{})
		if err != nil {
			return err
		}
		logf("%s is a member of environment %s (Rancher Project %s, binding %s/%s)", who, s.env(), project, bindingNS, created.GetName())
		return nil
	}
	rt := map[string]string{"owner": "cluster-owner", "member": "cluster-member"}[role]
	if rt == "" {
		return fmt.Errorf("--role %q: with Rancher, owner or member (Rancher's Cluster Owner and Member)", role)
	}
	if cluster == "" {
		return errors.New("with Rancher, --cluster is required (Rancher grants cluster roles per cluster; for an environment, use --env-member)")
	}
	rc, err := clusters.RancherCluster(ctx, s.kube.Dynamic, s.env(), cluster)
	if err != nil {
		return err
	}
	if rc == nil {
		return fmt.Errorf("cluster %s isn't imported into Rancher yet", cluster)
	}
	obj := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "management.cattle.io/v3", "kind": "ClusterRoleTemplateBinding",
		"metadata":         map[string]any{"generateName": "hyve-", "namespace": rc.GetName()},
		"clusterName":      rc.GetName(),
		"roleTemplateName": rt,
	}}
	for k, v := range subject {
		obj.Object[k] = v
	}
	created, err := s.kube.Dynamic.Resource(crtbGVR).Namespace(rc.GetName()).Create(ctx, obj, metav1.CreateOptions{})
	if err != nil {
		return err
	}
	logf("%s is %s on cluster %s (Rancher %s, binding %s/%s)", who, role, cluster, rc.GetName(), rc.GetName(), created.GetName())
	return nil
}

func rancherAccessList(ctx context.Context, s *session) error {
	cl, err := s.kube.Dynamic.Resource(clusters.ClusterGVR).Namespace(s.env()).List(ctx, metav1.ListOptions{})
	if err != nil {
		return err
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
	fmt.Fprintln(w, "BINDING\tSUBJECT\tROLE\tON")
	for _, c := range cl.Items {
		rc, err := clusters.RancherCluster(ctx, s.kube.Dynamic, c.GetNamespace(), c.GetName())
		if err != nil || rc == nil {
			continue
		}
		bs, err := s.kube.Dynamic.Resource(crtbGVR).Namespace(rc.GetName()).List(ctx, metav1.ListOptions{})
		if err != nil {
			continue
		}
		for _, b := range bs.Items {
			subj, _ := b.Object["userName"].(string)
			if g, _ := b.Object["groupPrincipalName"].(string); g != "" {
				subj = "group " + g
			} else {
				subj = "user " + subj
			}
			fmt.Fprintf(w, "%s/%s\t%s\t%v\t%s\n", b.GetNamespace(), b.GetName(), subj, b.Object["roleTemplateName"], c.GetName())
		}
	}
	return w.Flush()
}

func rancherRevoke(ctx context.Context, s *session, ref string) error {
	ns, name, ok := strings.Cut(ref, "/")
	if !ok {
		return errors.New("with Rancher, give the binding as NAMESPACE/NAME from 'hyve access list'")
	}
	gvr := crtbGVR
	if strings.Contains(ns, "p-") && !strings.HasPrefix(ns, "c-") {
		gvr = prtbGVR // a project's backing namespace, local-p-...
	}
	if err := s.kube.Dynamic.Resource(gvr).Namespace(ns).Delete(ctx, name, metav1.DeleteOptions{}); err != nil {
		return err
	}
	logf("Revoked %s", ref)
	return nil
}
