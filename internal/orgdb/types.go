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

	// PasswordHash is this binding's bcrypt password hash (Milestone 10
	// Part C) — nil for every OIDC binding (SubjectTypeOIDC), which never
	// had a password of its own. Replaces the paired <identity>-credentials
	// Kubernetes Secret every local account previously needed — see
	// internal/api/credentials.go's LoadPasswordHash.
	PasswordHash *string

	// Email is an optional contact address, unique within Namespace when
	// set (see migrations/*/0003_binding_email.sql) — nil for a binding
	// nobody has ever set one on. Usable as an alternate login identifier
	// alongside Identity (see Server.handleLogin) — groundwork for the
	// platform's move toward email-driven flows generally.
	Email *string

	CreatedAt time.Time
}

// ReconcilingCluster is a physical Kubernetes cluster hyve's control plane
// can reconcile organizations' infrastructure against, distinct from the
// cluster hyve-api's own pods happen to run on. Kubeconfig holds the raw
// kubeconfig content directly (Milestone 10 Part C) — this table originally
// stored only a pointer to a Kubernetes Secret holding it, in the control
// plane's own home cluster, deliberately reusing Kubernetes Secret storage/
// RBAC over a new Postgres-side encryption-at-rest story (see the proposal
// doc's earlier reasoning). That assumed a home cluster always exists to
// host the Secret; Part C's own "hyve-api deployable with zero Kubernetes
// access" goal means it can't anymore. See the migration files' own
// reconciling_clusters comment for the accepted plaintext-at-rest tradeoff
// this reversal carries.
type ReconcilingCluster struct {
	ID         string
	Name       string
	Kubeconfig string

	// Reachable is nil until the first health check has run — a real,
	// distinct third state from "reachable" and "unreachable", not
	// defaulted to either.
	Reachable     *bool
	LastCheckedAt *time.Time
	LastError     *string
	// KubernetesVersion is this cluster's own reported version (client-go's
	// discovery.ServerVersion().GitVersion) — captured on the same
	// reachability check that sets Reachable/LastCheckedAt, nil under the
	// identical "never checked yet" convention, and left at its last known
	// value on a failed check (a transient unreachable blip shouldn't
	// erase a version that was successfully observed moments earlier).
	KubernetesVersion *string
	CreatedAt         time.Time
}

// SigningKey is hyve-api's own session-signing key (Milestone 10 Part C) —
// see the migration files' own signing_keys comment for why this replaced
// an operator-provisioned Kubernetes Secret. KeyMaterial is the raw key
// bytes, base64-encoded for TEXT-column storage.
type SigningKey struct {
	ID          string
	Namespace   string
	KeyMaterial string
	CreatedAt   time.Time
}

// Session is one `hyve env login` session (Milestone 10 Part D) — mirrors
// the retired HyveSession CRD's HyveSessionSpec field-for-field, see that
// type's own doc comment for the full design. TokenHash is
// hex(SHA-256(the raw session secret)), never the secret itself.
type Session struct {
	ID              string
	Subject         string
	TenantNamespace string
	TokenHash       string
	ExpiresAt       time.Time
	CreatedAt       time.Time
}

// DefaultEnvironmentName is the environment every organization gets
// automatically at creation time, and what an unscoped grant or resource
// request resolves to — see HYVE-ORGANIZATION-MODEL-PROPOSAL.md's
// "Decided" note under "What moves to Postgres".
const DefaultEnvironmentName = "default"
