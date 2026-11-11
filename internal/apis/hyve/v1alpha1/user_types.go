package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// UserSpec is a person who signs in to hyve's host endpoint with a username
// and password (HYVE-PROPOSAL.md, "Signing in").
// metadata.name is the username. The password never lives here: its bcrypt
// hash is a Secret in the host's namespace, owned by this User, the way
// Rancher keeps local passwords in cattle-local-user-passwords — see
// internal/signin.
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
	// `hyve user create` without --no-password-change.
	MustChangePassword bool `json:"mustChangePassword,omitempty"`

	// Provider is "oidc" for a user who signs in through the identity
	// provider (and has no password); empty for a password user.
	Provider string `json:"provider,omitempty"`
	// Subject is the identity provider's ID for the user (the ID token's
	// sub claim).
	Subject string `json:"subject,omitempty"`
	// Email is where the host sends the user's password resets and account
	// emails. For an identity provider's user, it's the address the
	// provider gave at the last sign-in.
	Email string `json:"email,omitempty"`

	// TrustedCI lets CI jobs sign in as this user with the ID token their
	// CI system gives each job (GitHub Actions, GitLab CI...), so the
	// pipeline keeps no secret: a token from Issuer, for Audience, whose
	// claims match one entry gets a one-hour session.
	// +optional
	TrustedCI []TrustedCI `json:"trustedCI,omitempty"`
}

// TrustedCI is a CI system's jobs that may sign in as a user.
type TrustedCI struct {
	// Name tells entries apart (in `hyve user trust list`, the UI and the
	// host's log).
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`
	// +kubebuilder:validation:MaxLength=63
	Name string `json:"name"`
	// Issuer is the CI system's OIDC issuer, such as
	// https://token.actions.githubusercontent.com or https://gitlab.com.
	// +kubebuilder:validation:Pattern=`^https://`
	Issuer string `json:"issuer"`
	// Audience the job's token must be issued for (default "hyve").
	// +optional
	Audience string `json:"audience,omitempty"`
	// Claims the token must have, such as repository: org/web and
	// ref: refs/heads/main. A '*' in a value matches anything
	// (ref: refs/tags/*). At least one is required, and one of them
	// must say which repository or project: sub, repository,
	// repository_owner, repository_id, project_path or namespace_path.
	// +kubebuilder:validation:MinProperties=1
	Claims map[string]string `json:"claims"`
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

// User is a hyve account.
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
