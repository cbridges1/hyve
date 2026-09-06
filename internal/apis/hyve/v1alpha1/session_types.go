package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// HyveSessionSpec is the durable, revocable record backing one `hyve login`
// session — see internal/api/auth_handlers.go. The actual session secret a
// client presents to POST /auth/refresh is never stored here, only its
// SHA-256 hash (TokenHash) — read access to this object alone can never
// reconstruct a working credential, the same principle behind every other
// credential this codebase stores (e.g. internal/api/password.go's bcrypt
// hashes).
type HyveSessionSpec struct {
	// Subject is the authenticated username this session belongs to.
	Subject string `json:"subject"`

	// TenantNamespace is which namespace this login was authenticated
	// into (see HYVE-MULTI-TENANCY-PLAN.md's "Phase 2" section) — NOT the
	// same thing as this object's own metadata.namespace, which is always
	// the install's control-plane namespace (hyve-system by convention)
	// regardless of which tenant logged in. Empty means the
	// control-plane namespace itself (a superadmin login). Carried here
	// so POST /auth/refresh can re-issue an access token for the same
	// namespace without the caller needing to resend it.
	TenantNamespace string `json:"tenantNamespace,omitempty"`

	// TokenHash is hex(SHA-256(the raw session secret)) — compared against
	// on every POST /auth/refresh call. See internal/api/token.go's
	// HashSessionSecret.
	TokenHash string `json:"tokenHash"`

	// ExpiresAt is this session's own absolute expiry (see SessionTTL) —
	// independent of, and much longer than, any individual access token's
	// TTL (see AccessTokenTTL). Once past, POST /auth/refresh stops
	// working regardless of whether this object still exists; `hyve login`
	// again is the only way forward.
	ExpiresAt metav1.Time `json:"expiresAt"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Namespaced,shortName=hsess
// +kubebuilder:printcolumn:name="Subject",type=string,JSONPath=`.spec.subject`
// +kubebuilder:printcolumn:name="Expires",type=string,JSONPath=`.spec.expiresAt`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// HyveSession is the Schema for the hyvesessions API — the durable,
// kubectl-visible record backing one cluster-mode login (see `hyve login`/
// `hyve logout`), matching how Rancher's own management.cattle.io/v3 Token
// resource and Dex's Kubernetes storage backend both keep auth/session
// state as cluster-native objects rather than a separate database. Deleting
// one immediately revokes that session: the next POST /auth/refresh against
// it fails, bounding how long a logged-out client's cached access token
// keeps working to that token's own short TTL (see AccessTokenTTL) — there
// is no way to instantly invalidate an already-issued access token itself,
// by design (see internal/api/token.go's doc comments on why access tokens
// stay stateless).
type HyveSession struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec HyveSessionSpec `json:"spec,omitempty"`
}

// +kubebuilder:object:root=true

// HyveSessionList contains a list of HyveSession.
type HyveSessionList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []HyveSession `json:"items"`
}

func init() {
	SchemeBuilder.Register(&HyveSession{}, &HyveSessionList{})
}
