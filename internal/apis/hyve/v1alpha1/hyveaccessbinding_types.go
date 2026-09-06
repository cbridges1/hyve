package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Role values for HyveAccessBindingSpec.Role.
const (
	RoleAdmin    = "admin"
	RoleReadOnly = "read-only"
	RoleCustom   = "custom"

	// RoleSuperadmin is the one intentionally cluster-scoped role in an
	// otherwise namespace-scoped system (see HYVE-MULTI-TENANCY-PLAN.md's
	// "Phase 2" section). A superadmin's own HyveAccessBinding lives in
	// the install's control-plane namespace (conventionally hyve-system,
	// whatever Server.Namespace is set to) rather than any tenant
	// namespace — logging in with no --org/namespace resolves there,
	// which is what gives a superadmin a home without inventing a second,
	// parallel cluster-scoped binding type. Only a superadmin can create
	// new environments (POST /environments) or reach a ClusterDefinition
	// whose access.method is "primary" (the host cluster itself).
	RoleSuperadmin = "superadmin"
)

// SubjectType values for HyveAccessBindingSubject.Type.
const (
	// SubjectTypeLocal is a username/password identity — Value is the
	// username, and the matching bcrypt password hash lives in a paired
	// Kubernetes Secret (see internal/api/auth's naming convention), never
	// inline in this CRD. The initial, always-available identity mechanism
	// — no external IdP required.
	SubjectTypeLocal = "local"

	// SubjectTypeOIDC is reserved for a future OIDC-backed identity (e.g.
	// Okta) — Value would be matched against an OIDC ID token's subject or
	// email claim. Not implemented yet; this constant exists so the schema
	// doesn't need to change when it is.
	SubjectTypeOIDC = "oidc"
)

// HyveAccessBindingSubject identifies the external identity this binding
// grants a role to. Type is a discriminator so more than one identity kind
// can be supported without a schema break — SubjectTypeLocal today,
// SubjectTypeOIDC reserved for later.
type HyveAccessBindingSubject struct {
	Type  string `json:"type"`
	Value string `json:"value"`
}

// ServiceAccountRef names the in-cluster ServiceAccount that
// PrimaryClusterProvider's TokenRequest call mints a token against for a
// caller matching this binding — see HYVE-CONTROLLER-ARCHITECTURE-PLAN.md's
// Phase 6.5. The two default roles (admin/read-only) point at hyve's own
// static hyve-access-admin/hyve-access-readonly ServiceAccounts; a custom
// role points at an operator-defined one instead.
type ServiceAccountRef struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
}

// HyveAccessBindingSpec maps one external identity to a role.
type HyveAccessBindingSpec struct {
	Subject           HyveAccessBindingSubject `json:"subject"`
	Role              string                   `json:"role"`
	ServiceAccountRef ServiceAccountRef        `json:"serviceAccountRef"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Namespaced,shortName=hab
// +kubebuilder:printcolumn:name="Subject",type=string,JSONPath=`.spec.subject.value`
// +kubebuilder:printcolumn:name="Role",type=string,JSONPath=`.spec.role`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// HyveAccessBinding is the Schema for the hyveaccessbindings API — maps an
// external identity (an OIDC subject/email today) to a role, resolved by
// internal/api's auth middleware on every authenticated request. Namespaced:
// each hyve install (controller+API pair) only reads/writes bindings in its
// own namespace, so multiple installs (tenants) sharing one cluster never
// see or resolve each other's identities. Was cluster-scoped originally;
// switched to namespaced once multi-tenant installs on a shared cluster
// became a real requirement (see HYVE-MULTI-TENANCY-PLAN.md).
type HyveAccessBinding struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec HyveAccessBindingSpec `json:"spec,omitempty"`
}

// +kubebuilder:object:root=true

// HyveAccessBindingList contains a list of HyveAccessBinding.
type HyveAccessBindingList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []HyveAccessBinding `json:"items"`
}

func init() {
	SchemeBuilder.Register(&HyveAccessBinding{}, &HyveAccessBindingList{})
}
