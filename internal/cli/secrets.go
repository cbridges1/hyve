package cli

import (
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// Creating, changing and deleting the Secrets resources copy to clusters.
// They're plain Kubernetes Secrets, written as the signed-in user; values
// come from the command line, the environment or files, and are never
// printed.

// secretValues are the value flags 'secrets create' and 'secrets set' share.
type secretValues struct {
	literals, envs, files []string
}

func (v *secretValues) add(cmd *cobra.Command) {
	f := cmd.Flags()
	f.StringArrayVar(&v.literals, "from-literal", nil, "key=value (repeatable; it lands in shell history, so prefer --from-env or --from-file for real secrets)")
	f.StringArrayVar(&v.envs, "from-env", nil, "KEY, or key=VARIABLE: the value of an environment variable (repeatable)")
	f.StringArrayVar(&v.files, "from-file", nil, "key=path, or a path (the key is the file's name): the file's contents (repeatable)")
}

func (v *secretValues) empty() bool {
	return len(v.literals)+len(v.envs)+len(v.files) == 0
}

// data returns the values, base64-encoded as a Secret's data holds them.
func (v *secretValues) data() (map[string]any, error) {
	out := map[string]any{}
	put := func(k string, raw []byte) error {
		if !validSecretKey(k) {
			return fmt.Errorf("%q isn't a valid Secret key: letters, digits, '-', '_' and '.'", k)
		}
		if _, dup := out[k]; dup {
			return fmt.Errorf("key %q is given twice", k)
		}
		out[k] = base64.StdEncoding.EncodeToString(raw)
		return nil
	}
	for _, kv := range v.literals {
		k, val, ok := strings.Cut(kv, "=")
		if !ok {
			return nil, fmt.Errorf("--from-literal %q: want key=value", kv)
		}
		if err := put(k, []byte(val)); err != nil {
			return nil, err
		}
	}
	for _, e := range v.envs {
		k, name, ok := strings.Cut(e, "=")
		if !ok {
			name = k
		}
		val, set := os.LookupEnv(name)
		if !set {
			return nil, fmt.Errorf("--from-env %s: %s isn't set", e, name)
		}
		if err := put(k, []byte(val)); err != nil {
			return nil, err
		}
	}
	for _, f := range v.files {
		k, path, ok := strings.Cut(f, "=")
		if !ok {
			k, path = filepath.Base(f), f
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		if err := put(k, raw); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func validSecretKey(k string) bool {
	if k == "" || len(k) > 253 || k == "." || k == ".." {
		return false
	}
	for _, r := range k {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.') {
			return false
		}
	}
	return true
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func secretsCreateCmd() *cobra.Command {
	var (
		namespace, typ string
		labels         []string
		vals           secretValues
	)
	cmd := &cobra.Command{
		Use:   "create NAME (--from-literal key=value | --from-env KEY | --from-file key=path)...",
		Short: "Create a Secret for resources to copy to clusters",
		Long: `Creates a Secret in the template namespace (or --namespace), for template
resources and cluster extras to name in their secrets. Values come from
--from-literal, environment variables (--from-env) or files (--from-file);
they're never printed.`,
		Example: `  CF_TOKEN=... hyve secrets create cloudflare-dns --from-env api-token=CF_TOKEN
  hyve secrets create registry --type kubernetes.io/dockerconfigjson --from-file .dockerconfigjson=$HOME/.docker/config.json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if vals.empty() {
				return errors.New("give at least one value: --from-literal, --from-env or --from-file")
			}
			data, err := vals.data()
			if err != nil {
				return err
			}
			lbls, err := parseLabels(labels)
			if err != nil {
				return err
			}
			s, err := newSession()
			if err != nil {
				return err
			}
			ns := namespace
			if ns == "" {
				ns = s.templateNS()
			}
			meta := map[string]any{"name": args[0], "namespace": ns}
			if len(lbls) > 0 {
				l := map[string]any{}
				for k, v := range lbls {
					l[k] = v
				}
				meta["labels"] = l
			}
			obj := &unstructured.Unstructured{Object: map[string]any{
				"apiVersion": "v1", "kind": "Secret", "metadata": meta, "type": typ, "data": data,
			}}
			if _, err := s.kube.Dynamic.Resource(secretGVR).Namespace(ns).Create(cmd.Context(), obj, metav1.CreateOptions{}); err != nil {
				if apierrors.IsAlreadyExists(err) {
					return fmt.Errorf("Secret %s/%s exists; change it with 'hyve secrets set'", ns, args[0])
				}
				return err
			}
			logf("Created Secret %s/%s (keys: %s)", ns, args[0], strings.Join(sortedKeys(data), ", "))
			return nil
		},
	}
	cmd.Flags().StringVarP(&namespace, "namespace", "n", "", "the Secret's namespace (default: the template namespace)")
	cmd.Flags().StringVar(&typ, "type", "Opaque", "the Secret's type")
	cmd.Flags().StringArrayVarP(&labels, "label", "l", nil, "label key=value (repeatable)")
	vals.add(cmd)
	return cmd
}

func secretsSetCmd() *cobra.Command {
	var (
		namespace string
		remove    []string
		vals      secretValues
	)
	cmd := &cobra.Command{
		Use:   "set NAME [--from-literal key=value | --from-env KEY | --from-file key=path]... [--remove KEY]...",
		Short: "Add, replace or remove keys in a Secret",
		Long: `Changes a Secret's keys: the values given are added or replace the keys of
the same name, --remove deletes keys, and the rest stay as they are.
Clusters get the change the next time Fleet copies the Secret.`,
		Example: `  CF_TOKEN=... hyve secrets set cloudflare-dns --from-env api-token=CF_TOKEN
  hyve secrets set registry --remove old-key`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if vals.empty() && len(remove) == 0 {
				return errors.New("nothing to change: give values, or --remove")
			}
			data, err := vals.data()
			if err != nil {
				return err
			}
			s, err := newSession()
			if err != nil {
				return err
			}
			ns := namespace
			if ns == "" {
				ns = s.templateNS()
			}
			ctx := cmd.Context()
			sec, err := s.kube.Dynamic.Resource(secretGVR).Namespace(ns).Get(ctx, args[0], metav1.GetOptions{})
			if err != nil {
				if apierrors.IsNotFound(err) {
					return fmt.Errorf("no Secret %s/%s; make it with 'hyve secrets create'", ns, args[0])
				}
				return err
			}
			cur, _, _ := unstructured.NestedMap(sec.Object, "data")
			if cur == nil {
				cur = map[string]any{}
			}
			for _, k := range remove {
				if _, ok := cur[k]; !ok {
					return fmt.Errorf("Secret %s/%s has no key %q", ns, args[0], k)
				}
				if _, ok := data[k]; ok {
					return fmt.Errorf("key %q is both set and removed", k)
				}
				delete(cur, k)
			}
			for k, v := range data {
				cur[k] = v
			}
			sec.Object["data"] = cur
			delete(sec.Object, "stringData")
			if _, err := s.kube.Dynamic.Resource(secretGVR).Namespace(ns).Update(ctx, sec, metav1.UpdateOptions{}); err != nil {
				return err
			}
			logf("Updated Secret %s/%s (keys: %s)", ns, args[0], dash(strings.Join(sortedKeys(cur), ", ")))
			return nil
		},
	}
	cmd.Flags().StringVarP(&namespace, "namespace", "n", "", "the Secret's namespace (default: the template namespace)")
	cmd.Flags().StringArrayVar(&remove, "remove", nil, "a key to delete (repeatable)")
	vals.add(cmd)
	return cmd
}

func secretsDeleteCmd() *cobra.Command {
	var namespace string
	cmd := &cobra.Command{
		Use:     "delete NAME",
		Aliases: []string{"rm"},
		Short:   "Delete a Secret (resources that name it report it missing)",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := newSession()
			if err != nil {
				return err
			}
			ns := namespace
			if ns == "" {
				ns = s.templateNS()
			}
			if err := s.kube.Dynamic.Resource(secretGVR).Namespace(ns).Delete(cmd.Context(), args[0], metav1.DeleteOptions{}); err != nil {
				return err
			}
			logf("Deleted Secret %s/%s", ns, args[0])
			return nil
		},
	}
	cmd.Flags().StringVarP(&namespace, "namespace", "n", "", "the Secret's namespace (default: the template namespace)")
	return cmd
}
