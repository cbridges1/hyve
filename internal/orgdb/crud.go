package orgdb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ErrNotFound is returned by every Get*/lookup method below when no row
// matches — callers translate this into a 404, the same convention
// client.Client's own apierrors.IsNotFound already establishes elsewhere
// in this codebase for the Kubernetes-backed lookups this package is
// replacing.
var ErrNotFound = errors.New("orgdb: not found")

// CreateOrganization inserts a new organization row. Callers are
// responsible for the surrounding Kubernetes namespace/RBAC provisioning
// and for creating the organization's default Environment — this method
// only ever writes the one row, so it can be composed into a single
// transaction with those alongside CreateEnvironment/CreateBinding (see
// Milestone 2 in HYVE-ORGANIZATION-MODEL-IMPLEMENTATION-PLAN.md).
func (s *Store) CreateOrganization(ctx context.Context, org Organization) (Organization, error) {
	if org.ID == "" {
		org.ID = newID()
	}
	_, err := s.exec(ctx, `
		INSERT INTO organizations (id, name, namespace, plan, metadata, reconciling_cluster_id, pending_deletion)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, org.ID, org.Name, org.Namespace, org.Plan, org.Metadata, org.ReconcilingClusterID, org.PendingDeletion)
	if err != nil {
		return Organization{}, fmt.Errorf("insert organization: %w", err)
	}
	return s.GetOrganization(ctx, org.ID)
}

// GetOrganization looks up an organization by id.
func (s *Store) GetOrganization(ctx context.Context, id string) (Organization, error) {
	row := s.queryRow(ctx, `
		SELECT id, name, namespace, plan, metadata,
		       reconciling_cluster_id, reconciling_cluster_migration_status,
		       pending_deletion, created_at
		FROM organizations WHERE id = ?
	`, id)
	return scanOrganization(row)
}

// GetOrganizationByName looks up an organization by its unique name.
func (s *Store) GetOrganizationByName(ctx context.Context, name string) (Organization, error) {
	row := s.queryRow(ctx, `
		SELECT id, name, namespace, plan, metadata,
		       reconciling_cluster_id, reconciling_cluster_migration_status,
		       pending_deletion, created_at
		FROM organizations WHERE name = ?
	`, name)
	return scanOrganization(row)
}

func scanOrganization(row *sql.Row) (Organization, error) {
	var org Organization
	err := row.Scan(
		&org.ID, &org.Name, &org.Namespace, &org.Plan, &org.Metadata,
		&org.ReconcilingClusterID, &org.ReconcilingClusterMigrationStatus,
		&org.PendingDeletion, &org.CreatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Organization{}, ErrNotFound
	}
	if err != nil {
		return Organization{}, fmt.Errorf("scan organization: %w", err)
	}
	return org, nil
}

// ListOrganizations returns every organization — used by GET /organizations
// (an admin UX listing) and by `hyve migrate cluster --namespace
// hyve-system` to enumerate every tenant to migrate alongside the control
// plane, replacing that command's former HyveEnvironment-CRD-based
// enumeration (see internal/migrate's own history for that swap).
func (s *Store) ListOrganizations(ctx context.Context) ([]Organization, error) {
	rows, err := s.query(ctx, `
		SELECT id, name, namespace, plan, metadata,
		       reconciling_cluster_id, reconciling_cluster_migration_status,
		       pending_deletion, created_at
		FROM organizations ORDER BY name
	`)
	if err != nil {
		return nil, fmt.Errorf("list organizations: %w", err)
	}
	defer rows.Close()

	var out []Organization
	for rows.Next() {
		var org Organization
		if err := rows.Scan(
			&org.ID, &org.Name, &org.Namespace, &org.Plan, &org.Metadata,
			&org.ReconcilingClusterID, &org.ReconcilingClusterMigrationStatus,
			&org.PendingDeletion, &org.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan organization: %w", err)
		}
		out = append(out, org)
	}
	return out, rows.Err()
}

// MarkOrganizationPendingDeletion flips an organization's pending_deletion
// flag to true — the durable, crash-safe signal
// HYVE-ORGANIZATION-MODEL-PROPOSAL.md's Namespace-finalizer design relies
// on: written before the Kubernetes namespace delete is even issued, so a
// process restart between the two has something to resume from (the next
// sweep over ListOrganizationsPendingDeletion picks it back up) rather than
// silently losing track of an in-flight deletion.
func (s *Store) MarkOrganizationPendingDeletion(ctx context.Context, id string) error {
	_, err := s.exec(ctx, `UPDATE organizations SET pending_deletion = ? WHERE id = ?`, true, id)
	if err != nil {
		return fmt.Errorf("mark organization pending deletion: %w", err)
	}
	return nil
}

// ListOrganizationsPendingDeletion returns every organization currently
// mid-deletion — the sweep set a periodic check (or the next relevant
// request) walks to see whether each one's Namespace has finished
// terminating yet (see DeleteOrganization's own doc comment for the step
// that follows).
func (s *Store) ListOrganizationsPendingDeletion(ctx context.Context) ([]Organization, error) {
	rows, err := s.query(ctx, `
		SELECT id, name, namespace, plan, metadata,
		       reconciling_cluster_id, reconciling_cluster_migration_status,
		       pending_deletion, created_at
		FROM organizations WHERE pending_deletion = ? ORDER BY name
	`, true)
	if err != nil {
		return nil, fmt.Errorf("list organizations pending deletion: %w", err)
	}
	defer rows.Close()

	var out []Organization
	for rows.Next() {
		var org Organization
		if err := rows.Scan(
			&org.ID, &org.Name, &org.Namespace, &org.Plan, &org.Metadata,
			&org.ReconcilingClusterID, &org.ReconcilingClusterMigrationStatus,
			&org.PendingDeletion, &org.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan organization: %w", err)
		}
		out = append(out, org)
	}
	return out, rows.Err()
}

// DeleteOrganization permanently removes an organization's row along with
// every environment and binding scoped to it — called only once the
// caller has confirmed the organization's Kubernetes Namespace is fully
// gone (a Get 404, per the finalizer design), never before: this is the
// final step, not the trigger. Bindings and environments are deleted
// first in one transaction, ahead of the organization row itself, purely
// to satisfy their own FK references to it (see 0001_init.sql) — none of
// this is a Kubernetes-side cascade, since bindings/environments are
// Postgres/SQLite rows, not objects that live inside the Namespace and so
// were never touched by Kubernetes' own namespace-termination cascade.
func (s *Store) DeleteOrganization(ctx context.Context, id string) error {
	org, err := s.GetOrganization(ctx, id)
	if err != nil {
		return err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op once Commit succeeds

	deleteBindings := rebind(s.driver, `DELETE FROM bindings WHERE namespace = ?`)
	if _, err := tx.ExecContext(ctx, deleteBindings, org.Namespace); err != nil {
		return fmt.Errorf("delete bindings for organization: %w", err)
	}

	deleteEnvs := rebind(s.driver, `DELETE FROM environments WHERE organization_id = ?`)
	if _, err := tx.ExecContext(ctx, deleteEnvs, org.ID); err != nil {
		return fmt.Errorf("delete environments for organization: %w", err)
	}

	deleteOrg := rebind(s.driver, `DELETE FROM organizations WHERE id = ?`)
	if _, err := tx.ExecContext(ctx, deleteOrg, org.ID); err != nil {
		return fmt.Errorf("delete organization: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}

// CreateOrganizationWithDefaults inserts an organization, its default
// environment, and (only if adminIdentity is non-empty) an initial admin
// Binding for that identity — all in one transaction, per
// HYVE-ORGANIZATION-MODEL-PROPOSAL.md's "one atomic Postgres transaction"
// decision. adminIdentity is optional here specifically because, as of
// Milestone 2, nothing reads Binding rows for authorization yet (that
// starts at Milestone 4) — a caller may pass "" and grant access the old
// way in the meantime, or pass a real identity to start populating the
// table its own future auth checks will read.
func (s *Store) CreateOrganizationWithDefaults(ctx context.Context, org Organization, adminIdentity, adminRole string) (Organization, Environment, error) {
	if org.ID == "" {
		org.ID = newID()
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Organization{}, Environment{}, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op once Commit succeeds

	insertOrg := rebind(s.driver, `INSERT INTO organizations (id, name, namespace, plan, metadata, reconciling_cluster_id, pending_deletion) VALUES (?, ?, ?, ?, ?, ?, ?)`)
	if _, err := tx.ExecContext(ctx, insertOrg, org.ID, org.Name, org.Namespace, org.Plan, org.Metadata, org.ReconcilingClusterID, org.PendingDeletion); err != nil {
		return Organization{}, Environment{}, fmt.Errorf("insert organization: %w", err)
	}

	envID := newID()
	insertEnv := rebind(s.driver, `INSERT INTO environments (id, organization_id, name, metadata) VALUES (?, ?, ?, ?)`)
	if _, err := tx.ExecContext(ctx, insertEnv, envID, org.ID, DefaultEnvironmentName, "{}"); err != nil {
		return Organization{}, Environment{}, fmt.Errorf("insert default environment: %w", err)
	}

	if adminIdentity != "" {
		insertBinding := rebind(s.driver, `
			INSERT INTO bindings (id, namespace, organization_id, environment_id, subject_type, identity, role, service_account_name, service_account_namespace)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		`)
		saName := ServiceAccountNameForRole(adminRole)
		if _, err := tx.ExecContext(ctx, insertBinding, newID(), org.Namespace, org.ID, envID, SubjectTypeLocal, adminIdentity, adminRole, saName, org.Namespace); err != nil {
			return Organization{}, Environment{}, fmt.Errorf("insert initial admin binding: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return Organization{}, Environment{}, fmt.Errorf("commit transaction: %w", err)
	}

	createdOrg, err := s.GetOrganization(ctx, org.ID)
	if err != nil {
		return Organization{}, Environment{}, err
	}
	createdEnv, err := s.GetEnvironmentByName(ctx, org.ID, DefaultEnvironmentName)
	if err != nil {
		return Organization{}, Environment{}, err
	}
	return createdOrg, createdEnv, nil
}

// CreateEnvironment inserts a new environment row scoped to organizationID.
func (s *Store) CreateEnvironment(ctx context.Context, env Environment) (Environment, error) {
	if env.ID == "" {
		env.ID = newID()
	}
	_, err := s.exec(ctx, `
		INSERT INTO environments (id, organization_id, name, metadata)
		VALUES (?, ?, ?, ?)
	`, env.ID, env.OrganizationID, env.Name, env.Metadata)
	if err != nil {
		return Environment{}, fmt.Errorf("insert environment: %w", err)
	}
	return s.GetEnvironmentByName(ctx, env.OrganizationID, env.Name)
}

// GetEnvironmentByName looks up an environment within one organization by
// its short name — the pair (organizationID, name) is what's actually
// unique, per the schema's own UNIQUE(organization_id, name) constraint.
func (s *Store) GetEnvironmentByName(ctx context.Context, organizationID, name string) (Environment, error) {
	row := s.queryRow(ctx, `
		SELECT id, organization_id, name, metadata, created_at
		FROM environments WHERE organization_id = ? AND name = ?
	`, organizationID, name)
	var env Environment
	err := row.Scan(&env.ID, &env.OrganizationID, &env.Name, &env.Metadata, &env.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Environment{}, ErrNotFound
	}
	if err != nil {
		return Environment{}, fmt.Errorf("scan environment: %w", err)
	}
	return env, nil
}

// ListEnvironments returns every environment for organizationID, ordered by
// name — used to resolve "no ?env= given" against an org with exactly one
// environment (the common case, per HYVE-ORGANIZATION-MODEL-PROPOSAL.md's
// own CLI/API surface section) and to list environments for an admin UI.
func (s *Store) ListEnvironments(ctx context.Context, organizationID string) ([]Environment, error) {
	rows, err := s.query(ctx, `
		SELECT id, organization_id, name, metadata, created_at
		FROM environments WHERE organization_id = ? ORDER BY name
	`, organizationID)
	if err != nil {
		return nil, fmt.Errorf("list environments: %w", err)
	}
	defer rows.Close()

	var out []Environment
	for rows.Next() {
		var env Environment
		if err := rows.Scan(&env.ID, &env.OrganizationID, &env.Name, &env.Metadata, &env.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan environment: %w", err)
		}
		out = append(out, env)
	}
	return out, rows.Err()
}

const bindingColumns = `id, namespace, organization_id, environment_id, subject_type, identity, role, service_account_name, service_account_namespace, created_at`

// ServiceAccountNameForRole is the role -> ServiceAccount convention
// preserved from the retired HyveAccessBindingSpec.ServiceAccountRef (see
// that type's own predecessor doc comment) — admin and superadmin share
// hyve-access-admin (a superadmin's real privilege comes from Role alone,
// not this field; this SA only matters if they also fetch an ordinary
// kubeconfig for some tenant's cluster), read-only gets
// hyve-access-readonly. A custom role has no default — callers creating
// one must supply their own operator-defined ServiceAccount name directly
// rather than through this helper.
func ServiceAccountNameForRole(role string) string {
	switch role {
	case "read-only":
		return "hyve-access-readonly"
	default: // "admin", "superadmin"
		return "hyve-access-admin"
	}
}

// rolePriority ranks roles for FindBindingBySubject's "if this identity
// somehow has more than one binding in the same namespace, which one
// governs coarse (non-environment-scoped) authorization checks"
// resolution — highest wins. Ties (e.g. two "admin" bindings, which
// UNIQUE(namespace, environment_id, subject_type, identity) prevents
// within one environment but not across two) fall back to whichever sorts
// first, which is fine: same role, no real difference which row is "the"
// match.
func rolePriority(role string) int {
	switch role {
	case "superadmin":
		return 4
	case "admin":
		return 3
	case "read-only":
		return 2
	default: // "custom"
		return 1
	}
}

// CreateBinding inserts a new RBAC grant, scoped to Namespace (required —
// the actual isolation boundary, see Binding's own doc comment).
// OrganizationID/EnvironmentID must both be set or both be nil — set
// together only when Namespace has a matching Organization (a caller
// creating an unscoped grant within a real Organization resolves
// EnvironmentID to that organization's DefaultEnvironmentName environment
// before calling this — see the proposal doc's "no wildcard grant"
// decision); nil together otherwise.
func (s *Store) CreateBinding(ctx context.Context, b Binding) (Binding, error) {
	if b.ID == "" {
		b.ID = newID()
	}
	if b.Namespace == "" {
		return Binding{}, fmt.Errorf("create binding: namespace is required")
	}
	if (b.OrganizationID == nil) != (b.EnvironmentID == nil) {
		return Binding{}, fmt.Errorf("create binding: organization_id and environment_id must both be set or both be nil, got organization_id=%v environment_id=%v", b.OrganizationID, b.EnvironmentID)
	}
	if b.SubjectType == "" {
		b.SubjectType = SubjectTypeLocal
	}
	_, err := s.exec(ctx, `
		INSERT INTO bindings (id, namespace, organization_id, environment_id, subject_type, identity, role, service_account_name, service_account_namespace)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, b.ID, b.Namespace, b.OrganizationID, b.EnvironmentID, b.SubjectType, b.Identity, b.Role, b.ServiceAccountName, b.ServiceAccountNamespace)
	if err != nil {
		return Binding{}, fmt.Errorf("insert binding: %w", err)
	}
	return s.getBindingByID(ctx, b.ID)
}

func scanBinding(row *sql.Row) (Binding, error) {
	var b Binding
	err := row.Scan(&b.ID, &b.Namespace, &b.OrganizationID, &b.EnvironmentID, &b.SubjectType, &b.Identity, &b.Role, &b.ServiceAccountName, &b.ServiceAccountNamespace, &b.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Binding{}, ErrNotFound
	}
	if err != nil {
		return Binding{}, fmt.Errorf("scan binding: %w", err)
	}
	return b, nil
}

func (s *Store) getBindingByID(ctx context.Context, id string) (Binding, error) {
	row := s.queryRow(ctx, `SELECT `+bindingColumns+` FROM bindings WHERE id = ?`, id)
	return scanBinding(row)
}

// FindBindingBySubject looks up (subjectType, identity)'s binding within
// namespace — the actual isolation boundary (see Binding's own doc
// comment), matching the retired CRD-based FindBindingBySubject's own
// namespace-scoped signature exactly. If the identity somehow has more
// than one binding in this namespace (multiple environment-scoped
// grants), the highest-privilege one is returned — see rolePriority —
// since this is the coarse, namespace-level lookup used by login and the
// ordinary role gate (RequireRole), not a per-environment-resource
// authorization check.
func (s *Store) FindBindingBySubject(ctx context.Context, namespace, subjectType, identity string) (Binding, error) {
	bindings, err := s.findBindingsByNamespace(ctx, namespace, identity)
	if err != nil {
		return Binding{}, err
	}
	var best *Binding
	for i := range bindings {
		if bindings[i].SubjectType != subjectType {
			continue
		}
		if best == nil || rolePriority(bindings[i].Role) > rolePriority(best.Role) {
			best = &bindings[i]
		}
	}
	if best == nil {
		return Binding{}, ErrNotFound
	}
	return *best, nil
}

// findBindingsByNamespace returns every binding for identity within
// namespace — shared by FindBindingBySubject (which then picks the
// highest-privilege match) and ListBindingsForScope (which returns every
// one, for an accounts listing).
func (s *Store) findBindingsByNamespace(ctx context.Context, namespace, identity string) ([]Binding, error) {
	rows, err := s.query(ctx, `SELECT `+bindingColumns+` FROM bindings WHERE namespace = ? AND identity = ?`, namespace, identity)
	if err != nil {
		return nil, fmt.Errorf("query bindings: %w", err)
	}
	defer rows.Close()
	return scanBindings(rows)
}

func scanBindings(rows *sql.Rows) ([]Binding, error) {
	var out []Binding
	for rows.Next() {
		var b Binding
		if err := rows.Scan(&b.ID, &b.Namespace, &b.OrganizationID, &b.EnvironmentID, &b.SubjectType, &b.Identity, &b.Role, &b.ServiceAccountName, &b.ServiceAccountNamespace, &b.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan binding: %w", err)
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// ListBindingsForScope returns every binding within namespace — used by
// GET /accounts. Unlike FindBindingBySubject, this doesn't collapse to one
// row per identity: an identity with grants in two different environments
// shows up twice, which is correct for an admin auditing exactly what's
// granted.
func (s *Store) ListBindingsForScope(ctx context.Context, namespace string) ([]Binding, error) {
	rows, err := s.query(ctx, `SELECT `+bindingColumns+` FROM bindings WHERE namespace = ? ORDER BY identity`, namespace)
	if err != nil {
		return nil, fmt.Errorf("list bindings: %w", err)
	}
	defer rows.Close()
	return scanBindings(rows)
}

// DeleteBinding removes one binding by id.
func (s *Store) DeleteBinding(ctx context.Context, id string) error {
	_, err := s.exec(ctx, `DELETE FROM bindings WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete binding: %w", err)
	}
	return nil
}

// CreateReconcilingCluster registers a new physical cluster. The
// kubeconfig itself is never passed to or stored by this method — only
// the Secret reference the caller already wrote it to (see
// HYVE-ORGANIZATION-MODEL-PROPOSAL.md's "Schema addition" for why).
func (s *Store) CreateReconcilingCluster(ctx context.Context, rc ReconcilingCluster) (ReconcilingCluster, error) {
	if rc.ID == "" {
		rc.ID = newID()
	}
	_, err := s.exec(ctx, `
		INSERT INTO reconciling_clusters (id, name, kubeconfig_secret_namespace, kubeconfig_secret_name)
		VALUES (?, ?, ?, ?)
	`, rc.ID, rc.Name, rc.KubeconfigSecretNamespace, rc.KubeconfigSecretName)
	if err != nil {
		return ReconcilingCluster{}, fmt.Errorf("insert reconciling cluster: %w", err)
	}
	return s.GetReconcilingCluster(ctx, rc.ID)
}

// GetReconcilingCluster looks up a reconciling cluster by id.
func (s *Store) GetReconcilingCluster(ctx context.Context, id string) (ReconcilingCluster, error) {
	row := s.queryRow(ctx, `
		SELECT id, name, kubeconfig_secret_namespace, kubeconfig_secret_name,
		       reachable, last_checked_at, last_error, created_at
		FROM reconciling_clusters WHERE id = ?
	`, id)
	var rc ReconcilingCluster
	err := row.Scan(
		&rc.ID, &rc.Name, &rc.KubeconfigSecretNamespace, &rc.KubeconfigSecretName,
		&rc.Reachable, &rc.LastCheckedAt, &rc.LastError, &rc.CreatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return ReconcilingCluster{}, ErrNotFound
	}
	if err != nil {
		return ReconcilingCluster{}, fmt.Errorf("scan reconciling cluster: %w", err)
	}
	return rc, nil
}

// SetReconcilingClusterHealth records the result of a reachability check —
// written by the API server's own periodic health-check loop (Milestone
// 6), never by the controller, which never touches this store at all.
func (s *Store) SetReconcilingClusterHealth(ctx context.Context, id string, reachable bool, checkErr error) error {
	var lastError *string
	if checkErr != nil {
		msg := checkErr.Error()
		lastError = &msg
	}
	now := time.Now().UTC()
	_, err := s.exec(ctx, `
		UPDATE reconciling_clusters SET reachable = ?, last_checked_at = ?, last_error = ? WHERE id = ?
	`, reachable, now, lastError, id)
	if err != nil {
		return fmt.Errorf("update reconciling cluster health: %w", err)
	}
	return nil
}

// GetReconcilingClusterByName looks up a reconciling cluster by its unique
// name — used by POST /organizations' and PATCH /organizations/{name}'s
// own `reconcilingCluster` request field, which names a cluster the same
// way every other cross-reference in this API does, never by raw id.
func (s *Store) GetReconcilingClusterByName(ctx context.Context, name string) (ReconcilingCluster, error) {
	row := s.queryRow(ctx, `
		SELECT id, name, kubeconfig_secret_namespace, kubeconfig_secret_name,
		       reachable, last_checked_at, last_error, created_at
		FROM reconciling_clusters WHERE name = ?
	`, name)
	var rc ReconcilingCluster
	err := row.Scan(
		&rc.ID, &rc.Name, &rc.KubeconfigSecretNamespace, &rc.KubeconfigSecretName,
		&rc.Reachable, &rc.LastCheckedAt, &rc.LastError, &rc.CreatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return ReconcilingCluster{}, ErrNotFound
	}
	if err != nil {
		return ReconcilingCluster{}, fmt.Errorf("scan reconciling cluster: %w", err)
	}
	return rc, nil
}

// ListReconcilingClusters returns every registered reconciling cluster —
// GET /reconciling-clusters' own listing, and the set the API server's
// periodic health-check loop walks each tick.
func (s *Store) ListReconcilingClusters(ctx context.Context) ([]ReconcilingCluster, error) {
	rows, err := s.query(ctx, `
		SELECT id, name, kubeconfig_secret_namespace, kubeconfig_secret_name,
		       reachable, last_checked_at, last_error, created_at
		FROM reconciling_clusters ORDER BY name
	`)
	if err != nil {
		return nil, fmt.Errorf("list reconciling clusters: %w", err)
	}
	defer rows.Close()

	var out []ReconcilingCluster
	for rows.Next() {
		var rc ReconcilingCluster
		if err := rows.Scan(
			&rc.ID, &rc.Name, &rc.KubeconfigSecretNamespace, &rc.KubeconfigSecretName,
			&rc.Reachable, &rc.LastCheckedAt, &rc.LastError, &rc.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan reconciling cluster: %w", err)
		}
		out = append(out, rc)
	}
	return out, rows.Err()
}

// ListOrganizationsByReconcilingCluster returns every organization mapped
// to reconcilingClusterID — used by `cmd/controller/run.go`'s own
// `--reconciling-cluster-id` startup filter (a read-only Store query, the
// one deliberate, narrow exception to "the controller never touches
// Store" the proposal calls out: this is the sole reason a controller
// process needs to know its own reconciling-cluster identity at all)
// to determine which namespaces this particular controller process should
// watch/reconcile. nil means "the control plane's own home cluster" —
// every organization that has never been migrated anywhere else.
func (s *Store) ListOrganizationsByReconcilingCluster(ctx context.Context, reconcilingClusterID *string) ([]Organization, error) {
	var rows *sql.Rows
	var err error
	if reconcilingClusterID == nil {
		rows, err = s.query(ctx, `
			SELECT id, name, namespace, plan, metadata,
			       reconciling_cluster_id, reconciling_cluster_migration_status,
			       pending_deletion, created_at
			FROM organizations WHERE reconciling_cluster_id IS NULL ORDER BY name
		`)
	} else {
		rows, err = s.query(ctx, `
			SELECT id, name, namespace, plan, metadata,
			       reconciling_cluster_id, reconciling_cluster_migration_status,
			       pending_deletion, created_at
			FROM organizations WHERE reconciling_cluster_id = ? ORDER BY name
		`, *reconcilingClusterID)
	}
	if err != nil {
		return nil, fmt.Errorf("list organizations by reconciling cluster: %w", err)
	}
	defer rows.Close()

	var out []Organization
	for rows.Next() {
		var org Organization
		if err := rows.Scan(
			&org.ID, &org.Name, &org.Namespace, &org.Plan, &org.Metadata,
			&org.ReconcilingClusterID, &org.ReconcilingClusterMigrationStatus,
			&org.PendingDeletion, &org.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan organization: %w", err)
		}
		out = append(out, org)
	}
	return out, rows.Err()
}

// SetOrganizationMigrationStatus sets or clears (pass nil) an
// organization's reconciling_cluster_migration_status — 'migrating' locks
// every request against it to 423 (see RequireOrganizationNotMigrating)
// for the duration of a PATCH /organizations/{name} reconciling-cluster
// move, per the proposal's decided lock-don't-dual-serve design.
func (s *Store) SetOrganizationMigrationStatus(ctx context.Context, id string, status *string) error {
	_, err := s.exec(ctx, `UPDATE organizations SET reconciling_cluster_migration_status = ? WHERE id = ?`, status, id)
	if err != nil {
		return fmt.Errorf("set organization migration status: %w", err)
	}
	return nil
}

// AnyOrganizationHasReconcilingCluster reports whether any organization is
// currently mapped to a reconciling cluster other than the control
// plane's own home cluster — `cmd/api/run.go`'s own deployment-gate check
// (Milestone 7's SQLite/Postgres gate, made real here per the proposal's
// own decision): once true, a `--db=sqlite` API process should refuse to
// start, since Milestone 6's cross-organization-database migration
// primitive assumes a real, concurrently-accessible Postgres backend, not
// a single-file SQLite database.
func (s *Store) AnyOrganizationHasReconcilingCluster(ctx context.Context) (bool, error) {
	row := s.queryRow(ctx, `SELECT EXISTS(SELECT 1 FROM organizations WHERE reconciling_cluster_id IS NOT NULL)`)
	var exists bool
	if err := row.Scan(&exists); err != nil {
		return false, fmt.Errorf("check organizations for reconciling cluster: %w", err)
	}
	return exists, nil
}

// SetOrganizationReconcilingCluster flips an organization's
// reconciling_cluster_id to reconcilingClusterID (nil moves it back to the
// control plane's own home cluster) and clears
// reconciling_cluster_migration_status in the same write — called only
// once PATCH /organizations/{name}'s own migration copy is confirmed
// complete.
func (s *Store) SetOrganizationReconcilingCluster(ctx context.Context, id string, reconcilingClusterID *string) error {
	_, err := s.exec(ctx, `
		UPDATE organizations SET reconciling_cluster_id = ?, reconciling_cluster_migration_status = NULL WHERE id = ?
	`, reconcilingClusterID, id)
	if err != nil {
		return fmt.Errorf("set organization reconciling cluster: %w", err)
	}
	return nil
}
