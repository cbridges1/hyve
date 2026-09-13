package v1alpha1

// Role values for a Binding's Role (internal/orgdb) — kept here, in the
// CRD types package, rather than moved into internal/orgdb itself,
// because these are referenced throughout internal/api's role-gating
// (RequireRole) independent of where a binding's own data lives, and
// internal/orgdb deliberately doesn't import internal/apis/hyve/v1alpha1
// (the reverse dependency would be backwards — this package has no
// business knowing about a specific persistence layer). Originally
// HyveAccessBindingSpec.Role's own value set, before
// HYVE-ORGANIZATION-MODEL-IMPLEMENTATION-PLAN.md's Milestone 4 moved
// bindings out of this package's CRDs entirely (see internal/orgdb.Binding
// and its own SubjectType constants, which took over the CRD-era
// SubjectTypeLocal/SubjectTypeOIDC role this file used to also hold).
const (
	RoleAdmin    = "admin"
	RoleReadOnly = "read-only"
	RoleCustom   = "custom"

	// RoleSuperadmin is the one intentionally cluster-scoped role in an
	// otherwise namespace-scoped system (see HYVE-MULTI-TENANCY-PLAN.md's
	// "Phase 2" section). A superadmin's own binding lives in the
	// install's control-plane namespace (conventionally hyve-system,
	// whatever Server.Namespace is set to) rather than any tenant
	// namespace — logging in with no --org/namespace resolves there,
	// which is what gives a superadmin a home without inventing a second,
	// parallel cluster-scoped binding shape. Only a superadmin can create
	// new organizations (POST /organizations) or reach a ClusterDefinition
	// whose access.method is "primary" (the host cluster itself).
	RoleSuperadmin = "superadmin"
)

// ServiceAccountRef names a role -> ServiceAccount convention — the two
// default roles (admin/read-only) point at hyve's own static
// hyve-access-admin/hyve-access-readonly ServiceAccounts; a custom role
// points at an operator-defined one instead. Used directly by
// internal/api.HostProvider.HostServiceAccountRef (an ordinary Go field,
// not a CRD spec) — kept here as a plain struct rather than moved
// elsewhere purely to avoid a wider import-path churn for something this
// small. Originally also embedded in the now-retired
// HyveAccessBindingSpec; internal/orgdb.Binding's own
// ServiceAccountName/ServiceAccountNamespace fields cover that role for
// bindings today.
type ServiceAccountRef struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
}
