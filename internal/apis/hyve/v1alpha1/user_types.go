package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// UserSpec is a person who signs in to hyve's host endpoint with a username
// and password when Rancher isn't installed (HYVE-PROPOSAL.md, "Signing in").
// metadata.name is the username. The password never lives here: its bcrypt
// hash is a Secret in the host's namespace, owned by this User, the way
// Rancher keeps local passwords in cattle-local-user-passwords — see
// internal/hostauth.
type UserSpec struct {
	// DisplayName is shown in the UI and by `hyve user list`.
	DisplayName string `json:"displayName,omitempty"`

	// Groups the user belongs to. Kubernetes sees each one as
	// "hyve:<group>", alongside the user's own "hyve:<username>".
	Groups []string `json:"groups,omitempty"`

	// Disabled users can't sign in, and their existing sessions stop
	// working.
	Disabled bool `json:"disabled,omitempty"`

	// MustChangePassword makes the next sign-in change the password before
	// it's given a session. Set on the bootstrap admin and by
	// `hyve user add` without --no-password-change.
	MustChangePassword bool `json:"mustChangePassword,omitempty"`
}

// UserStatus is written by the host endpoint.
type UserStatus struct {
	// LastLogin is the time of the last successful sign-in.
	LastLogin *metav1.Time `json:"lastLogin,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster
// +kubebuilder:validation:XValidation:rule="self.metadata.name.size() <= 63 && self.metadata.name.matches('^[a-z0-9]([-a-z0-9]*[a-z0-9])?$')",message="a username must be a DNS label: lowercase letters, digits and '-', at most 63 characters"
// +kubebuilder:printcolumn:name="Display Name",type=string,JSONPath=`.spec.displayName`
// +kubebuilder:printcolumn:name="Disabled",type=boolean,JSONPath=`.spec.disabled`
// +kubebuilder:printcolumn:name="Last Login",type=date,JSONPath=`.status.lastLogin`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// User is a hyve account for standalone and local installs. With Rancher
// installed, Rancher's users are used instead and no Users exist.
type User struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   UserSpec   `json:"spec,omitempty"`
	Status UserStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// UserList contains a list of User.
type UserList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []User `json:"items"`
}

func init() {
	SchemeBuilder.Register(&User{}, &UserList{})
}
