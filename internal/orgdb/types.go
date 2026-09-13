package orgdb

import "time"

// Organization is one tenant's business record — name, plan/metadata, and
// the org-id <-> namespace-name mapping. The isolation boundary itself
// (the Kubernetes Namespace) is not part of this struct; this is purely
// the record Postgres/SQLite owns. See HYVE-ORGANIZATION-MODEL-PROPOSAL.md
// (nexus-config/docs) for the full design.
type Organization struct {
	ID        string
	Name      string
	Namespace string
	Plan      string
	Metadata  string // opaque JSON blob; callers decode as needed

	// ReconcilingClusterID is nil for "this control plane's own home
	// cluster" (the default, and the only value that exists before
	// Milestone 6 wires per-organization reconciling clusters up for
	// real).
	ReconcilingClusterID              *string
	ReconcilingClusterMigrationStatus *string
	PendingDeletion                   bool
	CreatedAt                         time.Time
}

// Environment is a named sub-scope within one Organization's namespace —
// the capability this whole schema exists to add. Every organization gets
// a `default` environment automatically at creation time.
type Environment struct {
	ID             string
	OrganizationID string
	Name           string
	Metadata       string
	CreatedAt      time.Time
}

// SubjectType values for Binding.SubjectType — mirrors the retired
// HyveAccessBindingSubject.Type exactly (SubjectTypeLocal/SubjectTypeOIDC).
const (
	SubjectTypeLocal = "local"
	SubjectTypeOIDC  = "oidc"
)

// Binding is one RBAC grant: identity + role, scoped to a Namespace — the
// actual isolation boundary, always set, mirroring the retired CRD-based
// HyveAccessBinding's own namespace scoping exactly, so isolation between
// two namespaces never depends on whether either has a registered
// Organization.
//
// OrganizationID/EnvironmentID are optional metadata layered on top, used
// only to resolve which Environment an unscoped grant within a real
// Organization defaults to (decided: no org-wide/wildcard grant shape
// within an organization — see the proposal doc's "What moves to
// Postgres" section) — set together when Namespace has a matching
// Organization, nil together otherwise (a self-hosted single-tenant
// install that never ran POST /organizations, or a superadmin binding,
// which has no Organization by definition).
type Binding struct {
	ID             string
	Namespace      string
	OrganizationID *string
	EnvironmentID  *string
	SubjectType    string
	Identity       string
	Role           string

	// ServiceAccountName/Namespace preserve the retired
	// HyveAccessBindingSpec.ServiceAccountRef — a role -> ServiceAccount
	// convention (see that type's own predecessor doc comment for why
	// it's kept even though nothing currently reads it back through the
	// mechanism it originally served).
	ServiceAccountName      string
	ServiceAccountNamespace string

	CreatedAt time.Time
}

// ReconcilingCluster is a physical Kubernetes cluster hyve's control plane
// can reconcile organizations' infrastructure against, distinct from the
// cluster hyve-api's own pods happen to run on. The kubeconfig itself is
// never stored here — only a pointer to the Secret holding it, in the
// control plane's own home cluster (see the proposal doc's own reasoning
// for reusing Kubernetes Secret storage/RBAC over a new Postgres-side
// encryption-at-rest story).
type ReconcilingCluster struct {
	ID                        string
	Name                      string
	KubeconfigSecretNamespace string
	KubeconfigSecretName      string

	// Reachable is nil until the first health check has run — a real,
	// distinct third state from "reachable" and "unreachable", not
	// defaulted to either.
	Reachable     *bool
	LastCheckedAt *time.Time
	LastError     *string
	CreatedAt     time.Time
}

// DefaultEnvironmentName is the environment every organization gets
// automatically at creation time, and what an unscoped grant or resource
// request resolves to — see HYVE-ORGANIZATION-MODEL-PROPOSAL.md's
// "Decided" note under "What moves to Postgres".
const DefaultEnvironmentName = "default"
