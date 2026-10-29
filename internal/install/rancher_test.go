package install

import "testing"

func TestRancherMatchers(t *testing.T) {
	for _, g := range []string{"management.cattle.io", "turtles-capi.cattle.io", "rke-machine-config.cattle.io", "ext.cattle.io", "herd.io"} {
		if !rancherGroups.MatchString(g) {
			t.Errorf("group %s should go", g)
		}
	}
	// Fleet's, and k3s's own.
	for _, g := range []string{"fleet.cattle.io", "helm.cattle.io", "k3s.cattle.io", "cluster.x-k8s.io", "hyve.io"} {
		if rancherGroups.MatchString(g) {
			t.Errorf("group %s must stay", g)
		}
	}
	for _, n := range []string{"cattle-globalrole-user", "crb-f4f5jbljff", "grb-u-b4qkhsnliz-view", "p-rg8lj-namespaces-edit", "pod-impersonation-helm-op-4x6hc", "rancher-webhook", "cluster-owner", "user-vx24j-view"} {
		if !rancherRBAC.MatchString(n) {
			t.Errorf("RBAC %s should go", n)
		}
	}
	// k3s labels these with objectset.rio.cattle.io, which is why RBAC is
	// matched by name; request-*-content is Fleet's agent's access to its
	// bundles.
	for _, n := range []string{"request-gcmxd-content", "system:coredns", "local-path-provisioner-role", "k3s-cloud-controller-manager", "clustercidrs-node", "cluster-admin", "capi-manager-role", "hyve-controller"} {
		if rancherRBAC.MatchString(n) {
			t.Errorf("RBAC %s must stay", n)
		}
	}
	for _, n := range []string{"local", "p-6877n", "user-dxcbx", "u-mo773yttt4"} {
		if !rancherNamespaceRE.MatchString(n) {
			t.Errorf("namespace %s should go", n)
		}
	}
	for _, n := range []string{"fleet-local", "fleet-default", "cattle-fleet-system", "cattle-capi-system", "capi-clusters", "plex", "user-data"} {
		if rancherNamespaceRE.MatchString(n) || contains(rancherNamespaces, n) {
			t.Errorf("namespace %s must stay", n)
		}
	}
}
