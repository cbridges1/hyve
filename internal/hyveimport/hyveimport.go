// Package hyveimport converts hyve Templates that build clusters with
// hyve-capi-module into hyve ClusterTemplates (hyve.io). Anything hyve can't express
// is reported as a note on the result rather than dropped silently.
package hyveimport

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/yaml"

	"github.com/cbridges1/hyve/internal/resources"
	"github.com/cbridges1/hyve/internal/templates"
)

// Result is one hyve Template's conversion.
type Result struct {
	Source   string // namespace/name, or the file it came from
	Template *templates.Template
	// Skipped is set (and Template nil) when the template can't be imported.
	Skipped string
	Notes   []string
}

func (r *Result) note(format string, args ...any) {
	r.Notes = append(r.Notes, fmt.Sprintf(format, args...))
}

// IsCAPIModule reports whether a driver source is hyve-capi-module (any
// spelling: github.com/<owner>/hyve-capi-module, a local path, ...).
func IsCAPIModule(source string) bool { return moduleIs(source, "hyve-capi-module") }

func moduleIs(source, module string) bool {
	return strings.HasSuffix(strings.TrimSuffix(source, "/"), module)
}

// moduleParams maps a hyve module's params onto ClusterClass variables:
// param → (variable, kind) where kind is "string", "int" or "bool".
type paramMap map[string][2]string

// mapParams converts params by m; unknown params are reported.
func mapParams(r *Result, params map[string]string, m paramMap, vars map[string]any, lockMap map[string]string) {
	var unknown []string
	rancher := false
	for k, v := range params {
		target, ok := m[k]
		if strings.HasPrefix(k, "RANCHER_") {
			rancher = true
			continue
		}
		if !ok {
			unknown = append(unknown, k)
			continue
		}
		if v == "" {
			continue
		}
		switch target[1] {
		case "int":
			n, err := strconv.ParseInt(v, 10, 64)
			if err != nil {
				r.note("params.%s %q isn't a number; skipped", k, v)
				continue
			}
			vars[target[0]] = n
		case "bool":
			vars[target[0]] = v == "true"
		default:
			vars[target[0]] = v
		}
		lockMap[k] = target[0]
	}
	if rancher {
		r.note("RANCHER_* params dropped: clusters get their agents automatically, no registration step")
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		r.note("params not understood, dropped: %s", strings.Join(unknown, ", "))
	}
}

func templateParams(spec map[string]any) map[string]string {
	params := map[string]string{}
	if raw, ok := spec["params"].(map[string]any); ok {
		for k, v := range raw {
			params[k] = fmt.Sprint(v)
		}
	}
	return params
}

func baseTemplate(spec map[string]any, name, ns, class string) *templates.Template {
	t := &templates.Template{Name: name, Namespace: ns}
	t.Spec.ClusterClassRef.Name = class
	t.Spec.Description, _, _ = unstructured.NestedString(spec, "description")
	t.Spec.Description = strings.TrimSpace(strings.Join(strings.Fields(t.Spec.Description), " "))
	return t
}

// k8sVersion turns a provider version label (1.30.5-k3s1, v1.34.1-k3s1)
// into a Kubernetes version (v1.30.5); "" for anything else.
func k8sVersion(label string) string {
	m := regexp.MustCompile(`^v?(\d+\.\d+\.\d+)`).FindStringSubmatch(label)
	if m == nil {
		return ""
	}
	return "v" + m[1]
}

// convertCivo maps hyve-civo-module onto the civo ClusterClass
// (cluster-api-provider-civo).
func convertCivo(r *Result, _ *unstructured.Unstructured, spec map[string]any, name, ns string) *Result {
	params := templateParams(spec)
	t := baseTemplate(spec, name, ns, "civo")
	vars, lockMap := map[string]any{}, map[string]string{}
	label := params["kubernetes_version"]
	delete(params, "kubernetes_version")
	mapParams(r, params, paramMap{
		"node_size": {"nodeSize", "string"}, "node_count": {"nodeCount", "int"},
		"cni_plugin": {"cniPlugin", "string"}, "cluster_type": {"clusterType", "string"},
		"network_id": {"networkID", "string"}, "disable_traefik": {"disableTraefik", "bool"},
		"disable_metrics_server": {"disableMetricsServer", "bool"},
	}, vars, lockMap)
	if label != "" {
		vars["civoVersion"] = label
		lockMap["kubernetes_version"] = "civoVersion"
		t.Spec.Version = k8sVersion(label)
	}
	if region, _, _ := unstructured.NestedString(spec, "region"); region != "" {
		vars["region"] = region
	}
	if len(vars) > 0 {
		t.Spec.Variables = vars
	}
	return finish(r, t, spec, vars, lockMap)
}

// convertUnraid maps hyve-unraid-k3s-module onto the unraid ClusterClass
// (cluster-api-provider-unraid).
func convertUnraid(r *Result, _ *unstructured.Unstructured, spec map[string]any, name, ns string) *Result {
	params := templateParams(spec)
	t := baseTemplate(spec, name, ns, "unraid")
	vars, lockMap := map[string]any{}, map[string]string{}
	tag := params["k3s_version"]
	delete(params, "k3s_version")
	mapParams(r, params, paramMap{
		"ip_address": {"host", "string"}, "ssh_port": {"sshPort", "int"},
		"container_name": {"containerName", "string"}, "docker_socket": {"dockerSocket", "bool"},
	}, vars, lockMap)
	if tag != "" && tag != "latest" {
		t.Spec.Version = k8sVersion(tag)
	}
	if tag == "latest" {
		r.note("k3s_version latest: hyve pins a version (the unraid class's default) — set kubernetesVersion to change it")
	}
	if vars["host"] == nil {
		r.note("no ip_address: set the host variable when creating clusters")
	}
	if len(vars) > 0 {
		t.Spec.Variables = vars
	}
	return finish(r, t, spec, vars, lockMap)
}

// flavor mirrors hyve-capi-module's gen/spec.sh presets.
type flavor struct {
	class, version, workerType, labels string
	controlPlane                       int64
	machineType                        bool // sets the machineType variable from params.machine_type
}

var flavors = map[string]flavor{
	"capd": {class: "capd", version: "v1.34.8", workerType: "machineDeployment", labels: "cni=flannel", controlPlane: 1},
	"gke":  {class: "gke", version: "v1.35.8", workerType: "machinePool", machineType: true},
}

// Parameters hyve handles without comment: the management cluster is
// the one hyve runs on, and API servers are reached through the agent's tunnel.
var ignoredParams = map[string]string{
	"namespace":          "",
	"management":         "hyve's management cluster is the one it's installed on",
	"mgmt_kubeconfig":    "hyve's management cluster is the one it's installed on",
	"kind_cluster":       "hyve local up replaces the module's kind management mode",
	"kind_node_image":    "hyve local up replaces the module's kind management mode",
	"providers":          "install providers with `hyve provider add`",
	"capi_version":       "install providers with `hyve provider add`",
	"init_env":           "set provider variables with `hyve provider add --variable`",
	"cluster_class_path": "apply the ClusterClass to the management cluster (e.g. through Fleet)",
	"api_host":           "hyve reaches clusters through the agent's tunnel, so no extra certificate SANs are needed",
}

// Convert turns one hyve Template object into a ClusterTemplate named name
// in namespace ns.
func Convert(u *unstructured.Unstructured, name, ns string) *Result {
	r := &Result{Source: u.GetNamespace() + "/" + u.GetName()}
	spec, _, _ := unstructured.NestedMap(u.Object, "spec")
	source, _, _ := unstructured.NestedString(spec, "driver", "source")
	switch {
	case IsCAPIModule(source):
	case moduleIs(source, "hyve-civo-module"):
		return convertCivo(r, u, spec, name, ns)
	case moduleIs(source, "hyve-unraid-k3s-module"):
		return convertUnraid(r, u, spec, name, ns)
	default:
		r.Skipped = fmt.Sprintf("driver %q has no hyve equivalent — hyve builds clusters through Cluster API (capi, Civo and Unraid modules convert)", source)
		return r
	}
	params := map[string]string{}
	if raw, ok := spec["params"].(map[string]any); ok {
		for k, v := range raw {
			params[k] = fmt.Sprint(v)
		}
	}

	t := &templates.Template{Name: name, Namespace: ns}
	t.Spec.Description, _, _ = unstructured.NestedString(spec, "description")
	t.Spec.Description = strings.TrimSpace(strings.Join(strings.Fields(t.Spec.Description), " "))
	f, hasFlavor := flavors[params["flavor"]]
	if params["flavor"] != "" && !hasFlavor {
		r.Skipped = fmt.Sprintf("unknown params.flavor %q", params["flavor"])
		return r
	}
	t.Spec.ClusterClassRef.Name = firstNonEmpty(params["cluster_class"], f.class)
	if t.Spec.ClusterClassRef.Name == "" {
		r.Skipped = "no params.cluster_class (or flavor)"
		return r
	}
	t.Spec.Version = firstNonEmpty(params["kubernetes_version"], f.version)

	workers := &templates.Workers{
		Type:  firstNonEmpty(params["worker_type"], f.workerType),
		Class: params["worker_class"],
	}
	count := int64(1)
	if v := params["worker_count"]; v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			r.Skipped = fmt.Sprintf("params.worker_count %q isn't a number", v)
			return r
		}
		count = n
	}
	workers.Count = &count
	t.Spec.Workers = workers
	cp := f.controlPlane
	if v := params["control_plane_count"]; v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			r.Skipped = fmt.Sprintf("params.control_plane_count %q isn't a number", v)
			return r
		}
		cp = n
	}
	if cp > 0 {
		t.Spec.ControlPlane = &templates.Replicas{Replicas: cp}
	}

	// Topology variables: the flavor's, then params.variables (which win).
	vars := map[string]any{}
	if f.machineType {
		vars["machineType"] = firstNonEmpty(params["machine_type"], "e2-medium")
	}
	for _, kv := range strings.Split(params["variables"], ";") {
		kv = strings.TrimSpace(kv)
		if kv == "" {
			continue
		}
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			r.note("params.variables entry %q isn't name=value; skipped", kv)
			continue
		}
		var val any
		if err := yaml.Unmarshal([]byte(v), &val); err != nil {
			val = v
		}
		vars[strings.TrimSpace(k)] = val
	}
	if len(vars) > 0 {
		t.Spec.Variables = vars
	}

	labels := map[string]string{}
	for _, kv := range strings.Split(firstNonEmpty(params["labels"], f.labels), ",") {
		if k, v, ok := strings.Cut(strings.TrimSpace(kv), "="); ok && k != "" {
			labels[k] = v
		}
	}
	// The capd flavor's cni=flannel comes from the ClusterClass's
	// hyve.io/cluster-labels annotation; keep it explicit anyway.
	if len(labels) > 0 {
		t.Spec.Labels = labels
	}
	if c := params["pod_cidr"]; c != "" && c != "10.244.0.0/16" {
		r.note("params.pod_cidr %s: hyve uses 10.244.0.0/16 for every cluster", c)
	}
	if c := params["service_cidr"]; c != "" && c != "10.128.0.0/12" {
		r.note("params.service_cidr %s: hyve uses 10.128.0.0/12 for every cluster", c)
	}

	var ignored []string
	for k := range params {
		switch k {
		case "flavor", "cluster_class", "kubernetes_version", "worker_count", "worker_type", "worker_class",
			"control_plane_count", "variables", "labels", "pod_cidr", "service_cidr", "machine_type":
		default:
			if why, ok := ignoredParams[k]; ok {
				if why != "" {
					r.note("params.%s dropped: %s", k, why)
				}
			} else {
				ignored = append(ignored, k)
			}
		}
	}
	if len(ignored) > 0 {
		sort.Strings(ignored)
		r.note("params not understood, dropped: %s", strings.Join(ignored, ", "))
	}

	lockMap := map[string]string{}
	for k := range vars {
		lockMap[k] = k
	}
	if f.machineType {
		lockMap["machine_type"] = "machineType"
	}
	return finish(r, t, spec, vars, lockMap)
}

// finish adds what every module's template shares: schedule, locks
// (lockMap: hyve param → ClusterClass variable), workflows, resources.
func finish(r *Result, t *templates.Template, spec map[string]any, vars map[string]any, lockMap map[string]string) *Result {
	t.Spec.Schedule, _, _ = unstructured.NestedString(spec, "schedule")

	// lockParams: hyve locks topology variables only.
	if locks, ok := spec["lockParams"].([]any); ok {
		for _, l := range locks {
			p := fmt.Sprint(l)
			switch v, ok := lockMap[p]; {
			case ok && vars[v] != nil:
				t.Spec.LockedVariables = append(t.Spec.LockedVariables, v)
			default:
				r.note("lockParams %q: hyve locks ClusterClass variables only; not locked", p)
			}
		}
	} else if all, ok := spec["lockParams"].(bool); ok && all {
		// hyve's --lock-params: every param fixed. hyve can lock the
		// ClusterClass variables the template sets.
		for k, v := range vars {
			if v != nil {
				t.Spec.LockedVariables = append(t.Spec.LockedVariables, k)
			}
		}
		sort.Strings(t.Spec.LockedVariables)
		r.note("lockParams: true — locked the template's variables (%s); hyve can't lock version or worker counts", strings.Join(t.Spec.LockedVariables, ", "))
	} else if v, ok := spec["lockParams"]; ok && v != nil && v != false {
		r.note("lockParams %v isn't a list of param names or true; ignored", v)
	}

	if wf, ok := spec["workflows"].(map[string]any); ok && len(wf) > 0 {
		var hooks, others []string
		for k, list := range wf {
			hooks = append(hooks, k)
			items, _ := list.([]any)
			for _, it := range items {
				n := fmt.Sprint(it)
				if m, ok := it.(map[string]any); ok {
					n = fmt.Sprint(m["name"])
				}
				if n != "register-with-rancher" && n != "deregister-from-rancher" {
					others = append(others, n)
				}
			}
		}
		sort.Strings(hooks)
		sort.Strings(others)
		if len(others) == 0 {
			r.note("workflows (%s) only registered the cluster with Rancher — hyve does that automatically", strings.Join(hooks, ", "))
		} else {
			r.note("workflows %s (%s) aren't carried over: hyve has no workflows — turn their steps into template resources", strings.Join(others, ", "), strings.Join(hooks, ", "))
		}
	}

	if res, ok := spec["resources"].([]any); ok {
		for _, x := range res {
			m, ok := x.(map[string]any)
			if !ok {
				continue
			}
			src, msg := convertResource(m)
			if msg != "" {
				r.note("%s", msg)
			}
			if src != nil {
				t.Spec.Resources = append(t.Spec.Resources, *src)
			}
		}
	}
	r.Template = t
	return r
}

var (
	ghSource = regexp.MustCompile(`^(github\.com|gitlab\.com|bitbucket\.org)/([^/]+)/([^/]+?)(?:\.git)?//(.*?)(?:@(.+))?$`)
	nameRE   = regexp.MustCompile(`[^a-z0-9-]+`)
)

func resourceName(s string) string {
	n := strings.Trim(nameRE.ReplaceAllString(strings.ToLower(s), "-"), "-")
	if len(n) > 40 {
		n = strings.TrimRight(n[:40], "-")
	}
	return n
}

// convertResource maps one hyve spec.resources entry onto a Fleet spec.
func convertResource(m map[string]any) (*resources.Entry, string) {
	name := resourceName(fmt.Sprint(m["name"]))
	if d, _ := m["delete"].(bool); d {
		return nil, fmt.Sprintf("resource %s is marked delete: true; not imported", name)
	}
	if h, ok := m["helm"].(map[string]any); ok {
		helm := map[string]any{}
		for _, k := range []string{"chart", "repo", "version"} {
			if v := str(h[k]); v != "" {
				helm[k] = v
			}
		}
		if vals, ok := h["values"].(map[string]any); ok && len(vals) > 0 {
			helm["values"] = nestValues(vals)
		}
		return &resources.Entry{Name: name, Namespace: str(h["namespace"]), HelmOp: map[string]any{"helm": helm}}, ""
	}
	if s := str(m["source"]); s != "" {
		g := ghSource.FindStringSubmatch(s)
		if g == nil {
			return nil, fmt.Sprintf("resource %s: source %q isn't a git URL hyve can turn into a GitRepo; not imported", name, s)
		}
		git := map[string]any{"repo": fmt.Sprintf("https://%s/%s/%s.git", g[1], g[2], g[3])}
		if p := strings.Trim(g[4], "/"); p != "" {
			git["paths"] = []any{p}
		}
		if ref := g[5]; ref != "" {
			if regexp.MustCompile(`^[0-9a-f]{7,40}$`).MatchString(ref) {
				git["revision"] = ref
			} else {
				git["branch"] = ref
			}
		}
		return &resources.Entry{Name: name, Namespace: str(m["namespace"]), GitRepo: git}, ""
	}
	if _, ok := m["secret"]; ok {
		return nil, fmt.Sprintf("resource %s is a secret entry; create the Secret in the template namespace and list it under a resource's secrets", name)
	}
	return nil, fmt.Sprintf("resource %s: no helm or source; not imported", name)
}

// nestValues turns hyve's --set style keys ("a.b.c", "x\.y") into nested
// Helm values.
func nestValues(flat map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range flat {
		parts := splitUnescaped(k)
		m := out
		for _, p := range parts[:len(parts)-1] {
			next, ok := m[p].(map[string]any)
			if !ok {
				next = map[string]any{}
				m[p] = next
			}
			m = next
		}
		m[parts[len(parts)-1]] = v
	}
	return out
}

func splitUnescaped(k string) []string {
	var parts []string
	var cur strings.Builder
	for i := 0; i < len(k); i++ {
		switch {
		case k[i] == '\\' && i+1 < len(k) && k[i+1] == '.':
			cur.WriteByte('.')
			i++
		case k[i] == '.':
			parts = append(parts, cur.String())
			cur.Reset()
		default:
			cur.WriteByte(k[i])
		}
	}
	return append(parts, cur.String())
}

func str(v any) string {
	if v == nil {
		return ""
	}
	return fmt.Sprint(v)
}

func firstNonEmpty(s ...string) string {
	for _, v := range s {
		if v != "" {
			return v
		}
	}
	return ""
}

// IsHerdTemplate reports whether an object is a herd ClusterTemplate
// (herd.io), which ConvertHerd moves to hyve.io.
func IsHerdTemplate(u *unstructured.Unstructured) bool {
	return u.GetKind() == "ClusterTemplate" && strings.HasPrefix(u.GetAPIVersion(), "herd.io/")
}

// ConvertHerd turns a herd ClusterTemplate into a hyve one in namespace ns:
// the same spec, with each resource's herd shorthand (helmOp: {repo,
// chart, ...}, gitRepo, bundle) rewritten as Fleet's own spec.
func ConvertHerd(u *unstructured.Unstructured, ns string) *Result {
	r := &Result{Source: u.GetNamespace() + "/" + u.GetName()}
	spec, _, _ := unstructured.NestedMap(u.Object, "spec")
	res, _ := spec["resources"].([]any)
	delete(spec, "resources")
	c := u.DeepCopy()
	c.Object["spec"] = spec
	t, err := templates.FromUnstructured(c)
	if err != nil {
		r.Skipped = err.Error()
		return r
	}
	t.Namespace = ns
	for _, x := range res {
		m, ok := x.(map[string]any)
		if !ok {
			continue
		}
		e := resources.Entry{Name: str(m["name"]), Namespace: str(m["namespace"])}
		drift, _ := m["correctDrift"].(bool)
		switch {
		case m["helmOp"] != nil:
			h, _ := m["helmOp"].(map[string]any)
			helm := map[string]any{}
			for k, v := range h {
				helm[k] = v
			}
			e.HelmOp = map[string]any{"helm": helm}
		case m["gitRepo"] != nil:
			g, _ := m["gitRepo"].(map[string]any)
			e.GitRepo = map[string]any{}
			for k, v := range g {
				e.GitRepo[k] = v
			}
		case m["bundle"] != nil:
			b, _ := m["bundle"].(map[string]any)
			// Read when applied, relative to the file (a hyve CLI convenience).
			e.Bundle = map[string]any{"path": str(b["path"])}
			r.note("resource %s: bundle from %s; apply the result with 'hyve template apply -f' from the template's directory so the manifests are found", e.Name, str(b["path"]))
		default:
			r.note("resource %s has no helmOp, gitRepo or bundle; not imported", e.Name)
			continue
		}
		if drift {
			for _, spec := range []map[string]any{e.HelmOp, e.GitRepo, e.Bundle} {
				if spec != nil {
					spec["correctDrift"] = map[string]any{"enabled": true}
				}
			}
		}
		t.Spec.Resources = append(t.Spec.Resources, e)
	}
	r.Template = t
	return r
}

// Converted is one object's conversion, named for the template namespace.
type Converted struct {
	*Result
	// Object is what was converted.
	Object *unstructured.Unstructured
	// Herd says Object is a herd ClusterTemplate: the clusters herd made
	// from it carry herd.io/template=<its name> in its namespace.
	Herd bool
}

// ConvertAll converts old hyve Templates and herd ClusterTemplates into
// ClusterTemplates in ns. Each keeps its old name, or becomes
// <old namespace>-<name> when prefixNS is set or two names would collide.
func ConvertAll(objs []*unstructured.Unstructured, ns string, prefixNS bool) []Converted {
	count := map[string]int{}
	for _, o := range objs {
		count[o.GetName()]++
	}
	sorted := append([]*unstructured.Unstructured(nil), objs...)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].GetNamespace()+"/"+sorted[i].GetName() < sorted[j].GetNamespace()+"/"+sorted[j].GetName()
	})
	var out []Converted
	for _, o := range sorted {
		name := o.GetName()
		if o.GetNamespace() != "" && (prefixNS || count[name] > 1) {
			name = o.GetNamespace() + "-" + name
		}
		c := Converted{Object: o, Herd: IsHerdTemplate(o)}
		if c.Herd {
			c.Result = ConvertHerd(o, ns)
			if c.Template != nil {
				c.Template.Name = name
			}
		} else {
			c.Result = Convert(o, name, ns)
		}
		out = append(out, c)
	}
	return out
}
