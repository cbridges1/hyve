package cli

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

var (
	accessGVR      = schema.GroupVersionResource{Group: "hyve.io", Version: "v1alpha1", Resource: "clusteraccesses"}
	roleBindingGVR = schema.GroupVersionResource{Group: "rbac.authorization.k8s.io", Version: "v1", Resource: "rolebindings"}
)

// AccessCmd is `hyve access`: who may use which clusters.
func AccessCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "access",
		Short: "Grant hyve users access to clusters and environments",
		Long: `Grants a hyve user or group a role on clusters, as Rancher's cluster role
bindings do: owner (everything), member (see the cluster, create namespaces)
or read-only (read everything but Secrets). Without --cluster it covers every
cluster in the environment, now and later. A cluster's creator is its owner
automatically.

--env-member lets them create and manage clusters in the environment.`,
	}
	AddGlobalFlags(cmd)
	addEnvFlag(cmd)
	addTemplateNSFlag(cmd)
	cmd.AddCommand(accessGrantCmd(), accessListCmd(), accessRevokeCmd())
	return cmd
}

func accessGrantCmd() *cobra.Command {
	var role, group, cluster string
	var envMember bool
	cmd := &cobra.Command{
		Use:   "grant [USER] (--role owner|member|read-only [--cluster NAME] | --env-member) [--group GROUP]",
		Short: "Grant a user or group a role on clusters, or membership of the environment",
		Example: `  hyve access grant alice --role owner --cluster web
  hyve access grant --group ops --role read-only          # every cluster in the environment
  hyve access grant bob --env-member -e ci`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			user := ""
			if len(args) == 1 {
				user = args[0]
			}
			if (user == "") == (group == "") {
				return errors.New("give a user or --group, not both")
			}
			if envMember == (role != "") {
				return errors.New("give --role or --env-member")
			}
			s, err := newSession()
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			subjectKind, subject, who := "User", "hyve:"+user, "user "+user
			if group != "" {
				subjectKind, subject, who = "Group", "hyve:"+group, "group "+group
			}
			name := strings.ToLower(subjectKind + "-" + strings.TrimPrefix(subject, "hyve:"))
			if envMember {
				// Members create clusters here and read the templates they're
				// made from.
				for _, rb := range []struct{ ns, role string }{{s.env(), "hyve-environment-member"}, {s.templateNS(), "hyve-template-reader"}} {
					obj := &unstructured.Unstructured{Object: map[string]any{
						"apiVersion": "rbac.authorization.k8s.io/v1", "kind": "RoleBinding",
						"metadata": map[string]any{"name": "hyve-member-" + name, "namespace": rb.ns, "labels": map[string]any{"app.kubernetes.io/managed-by": "hyve", "hyve.io/env-member": "true"}},
						"roleRef":  map[string]any{"apiGroup": "rbac.authorization.k8s.io", "kind": "ClusterRole", "name": rb.role},
						"subjects": []any{map[string]any{"apiGroup": "rbac.authorization.k8s.io", "kind": subjectKind, "name": subject}},
					}}
					if err := s.kube.ApplyObject(ctx, obj); err != nil {
						return err
					}
				}
				logf("%s is a member of environment %s", who, s.env())
				return nil
			}
			switch role {
			case "owner", "member", "read-only":
			default:
				return fmt.Errorf("--role %q: want owner, member or read-only", role)
			}
			caName := role + "-" + name
			spec := map[string]any{"role": role}
			if user != "" {
				spec["user"] = user
			} else {
				spec["group"] = group
			}
			if cluster != "" {
				if _, err := s.getCluster(ctx, cluster); err != nil {
					return err
				}
				spec["clusterName"] = cluster
				caName = cluster + "-" + caName
			}
			obj := &unstructured.Unstructured{Object: map[string]any{
				"apiVersion": "hyve.io/v1alpha1", "kind": "ClusterAccess",
				"metadata": map[string]any{"name": caName, "namespace": s.env()},
				"spec":     spec,
			}}
			if err := s.kube.ApplyObject(ctx, obj); err != nil {
				return notInstalled(err)
			}
			on := "every cluster in " + s.env()
			if cluster != "" {
				on = "cluster " + cluster
			}
			logf("%s is %s on %s (ClusterAccess %s)", who, role, on, caName)
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&role, "role", "", "owner, member or read-only")
	f.StringVar(&group, "group", "", "a hyve group instead of a user")
	f.StringVar(&cluster, "cluster", "", "one cluster (default: every cluster in the environment)")
	f.BoolVar(&envMember, "env-member", false, "let them create and manage clusters in the environment")
	return cmd
}

func accessListCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List grants in the environment",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, err := newSession()
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			list, err := s.kube.Dynamic.Resource(accessGVR).Namespace(s.env()).List(ctx, metav1.ListOptions{})
			if err != nil {
				return notInstalled(err)
			}
			w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
			fmt.Fprintln(w, "NAME\tSUBJECT\tROLE\tON\tREADY")
			for _, ca := range list.Items {
				get := func(f string) string { v, _, _ := unstructured.NestedString(ca.Object, "spec", f); return v }
				subj := "user " + get("user")
				if get("group") != "" {
					subj = "group " + get("group")
				}
				on := "every cluster"
				if get("clusterName") != "" {
					on = get("clusterName")
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", ca.GetName(), subj, get("role"), on, readyCondition(&ca))
			}
			rbs, err := s.kube.Dynamic.Resource(roleBindingGVR).Namespace(s.env()).List(ctx, metav1.ListOptions{LabelSelector: "hyve.io/env-member=true"})
			if err == nil {
				for _, rb := range rbs.Items {
					subs, _, _ := unstructured.NestedSlice(rb.Object, "subjects")
					for _, sub := range subs {
						m := sub.(map[string]any)
						fmt.Fprintf(w, "%s\t%s %s\tenvironment member\t%s\t-\n", rb.GetName(), strings.ToLower(fmt.Sprint(m["kind"])), strings.TrimPrefix(fmt.Sprint(m["name"]), "hyve:"), s.env())
					}
				}
			}
			return w.Flush()
		},
	}
}

func accessRevokeCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "revoke NAME",
		Aliases: []string{"rm"},
		Short:   "Remove a grant (a name from 'hyve access list')",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := newSession()
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			if strings.HasPrefix(args[0], "hyve-member-") {
				for _, ns := range []string{s.env(), s.templateNS()} {
					if err := s.kube.Dynamic.Resource(roleBindingGVR).Namespace(ns).Delete(ctx, args[0], metav1.DeleteOptions{}); err != nil && ns == s.env() {
						return err
					}
				}
				logf("Removed environment membership %s", args[0])
				return nil
			}
			if err := s.kube.Dynamic.Resource(accessGVR).Namespace(s.env()).Delete(ctx, args[0], metav1.DeleteOptions{}); err != nil {
				return notInstalled(err)
			}
			logf("Revoked %s; hyve-controller removes it from the clusters", args[0])
			return nil
		},
	}
}
