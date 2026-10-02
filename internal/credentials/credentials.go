// Package credentials holds the Kubernetes Secret naming convention for a
// local user's Binding (internal/orgdb) credentials — split out of
// internal/api specifically so internal/migrate can recognize these
// Secrets by name too, without importing internal/api's other, unrelated
// internals. That import would otherwise be a cycle: internal/migrate ->
// internal/api for this one naming convention, and internal/api ->
// internal/migrate for Milestone 6's PATCH /organizations/{name}
// reconciling-cluster migration, which reuses internal/migrate's own copy
// primitives (see HYVE-ORGANIZATION-MODEL-PROPOSAL.md's "Per-organization
// reconciling cluster" section, nexus-config/docs).
package credentials

// UserCredentialsSecretSuffix names the Secret paired with a local user's
// Binding (internal/orgdb), holding its bcrypt password hash — never
// stored inline on the binding row itself. Mirrors the
// <cluster-name>-access-kubeconfig Secret naming convention used elsewhere
// in this project.
const UserCredentialsSecretSuffix = "-credentials"

// UserCredentialsSecretName returns the paired Secret name for a local
// user's Binding (internal/orgdb).
func UserCredentialsSecretName(bindingName string) string {
	return bindingName + UserCredentialsSecretSuffix
}
