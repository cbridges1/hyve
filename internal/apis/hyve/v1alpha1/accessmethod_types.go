package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// AccessMethodSpec holds connection config for an externally-addressable
// identity/access service (Rancher, Teleport, ...) that mints kubeconfigs
// for clusters that reference it, plus a reference to the module that
// knows how to talk to it. hyve's own code has no idea what "Rancher" or
// "Teleport" means — Driver points at an ordinary auth-only module (see
// module.mdx's `--auth-only`). Its auth operation always runs
// server-side, inside a short-lived Job in Runner.Image (never on the
// caller's own machine — that was this design's first pass, replaced once
// it became clear an AccessMethod's whole point is often exactly the case
// a caller's own machine *can't* reach the identity service, or doesn't
// have its tools installed): see HYVE-ACCESS-METHOD-DESIGN.md's
// "Server-side dispatch" section for the full push-based relay mechanism
// that gets the resulting kubeconfig out without ever writing it to a
// Secret, a log, or persisting it in the cluster at all.
//
// AccessMethodSpec itself is never a credential — only where to reach the
// service, which module talks to it, and what image to run that module
// in. The credential comes from whichever user requests access, at
// request time (forwarded into the Job as a short-lived, owner-referenced
// Secret, deleted the moment the Job is), which is what makes this object
// itself safe to read broadly and safe to commit to a GitOps repo.
type AccessMethodSpec struct {
	// Driver identifies the auth-only module that implements this access
	// method — resolved server-side (the API's own module cache) and run
	// inside an ephemeral Job, never by hyve's own code. Mutually
	// exclusive with InlineAuth: exactly one of the two must be set.
	// Prefer Driver for anything meant to be versioned, reused across
	// multiple AccessMethods, or shared/published — it goes through the
	// same git-source resolution (hyve.lock, the module cache) a
	// ClusterDefinition's own driver does, so it's durable across API pod
	// restarts the way a local-path source or InlineAuth's own module-less
	// dispatch isn't automatically.
	Driver DriverRef `json:"driver,omitempty"`

	// InlineAuth, if set, is the complete auth-operation shell script run
	// directly — no module resolution at all (no git clone, no
	// ModulesDir/local-path lookup). For a small, self-contained access
	// method with no need to version or share it, this avoids needing
	// anywhere to host a module at all. Mutually exclusive with Driver.
	// Receives the exact same HYVE_CLUSTER_NAME/
	// HYVE_ACCESS_METHOD_SERVER_URL/HYVE_ACCESS_METHOD_CLUSTER_ID
	// environment a Driver-resolved module's auth operation gets, and is
	// wrapped in the exact same push-based relay (see
	// HYVE-ACCESS-METHOD-DESIGN.md) — the only difference is where the
	// script's text comes from.
	InlineAuth string `json:"inlineAuth,omitempty"`

	// RequiredEnv names the credential environment variables this access
	// method's auth operation needs (e.g. RANCHER_USERNAME,
	// RANCHER_PASSWORD) — `hyve cluster auth` reads exactly these names
	// from the caller's own local environment and forwards only them,
	// never the caller's whole environment. Only consulted when
	// InlineAuth is set; a Driver-resolved module instead declares this
	// via its own module.yaml's spec.requirements.env.
	RequiredEnv []string `json:"requiredEnv,omitempty"`

	// ServerURL is the identity/access service's own address — e.g. a
	// Rancher server's base URL. Passed to the auth operation as
	// HYVE_ACCESS_METHOD_SERVER_URL; meaning beyond that is entirely up to
	// the script/module, hyve never validates its shape beyond requiring
	// it be set. Must be reachable from inside the cluster's own pod
	// network (this always runs in a Job now) — not necessarily the same
	// address a caller's own machine would use.
	ServerURL string `json:"serverURL"`

	// Runner configures the image the Job runs the auth operation in —
	// same shape and same "admin picks an image with the right tools
	// already installed" convention ClusterDefinitionSpec.Runner already
	// has (RunnerSpec.Image empty falls back to
	// HyveConfig.spec.defaultModuleImage, same as any other module
	// operation's image resolution). Needs curl at minimum, regardless of
	// Driver vs InlineAuth — see buildMintWrapperScript.
	Runner RunnerSpec `json:"runner,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Namespaced,shortName=am
// +kubebuilder:printcolumn:name="Driver",type=string,JSONPath=`.spec.driver.source`
// +kubebuilder:printcolumn:name="ServerURL",type=string,JSONPath=`.spec.serverURL`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// AccessMethod is the Schema for the accessmethods API — an admin-managed,
// reusable, named resource referenced by name
// (ClusterDefinitionSpec.Access.AccessMethodRef) from any ClusterDefinition,
// decoupled from which module provisioned that cluster. Namespaced, for the
// same reason HyveAccessBinding is: a cluster-scoped object here would let
// one tenant's ClusterDefinition reference (and, depending on RBAC, list)
// another tenant's access-service config. Manageable both ways: directly
// via kubectl, or through the API's own admin-gated POST/PATCH/DELETE
// /access-methods (internal/api/accessmethods.go) — its mint operation is
// fully tenant-isolated, so API-side create/update carries no more
// sensitivity than Template/Workflow already do. See
// HYVE-ACCESS-METHOD-DESIGN.md for the full design.
type AccessMethod struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec AccessMethodSpec `json:"spec,omitempty"`
}

// +kubebuilder:object:root=true

// AccessMethodList contains a list of AccessMethod.
type AccessMethodList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []AccessMethod `json:"items"`
}

func init() {
	SchemeBuilder.Register(&AccessMethod{}, &AccessMethodList{})
}
