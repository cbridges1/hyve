package orgdb

import (
	"context"
	"fmt"
)

// MigrationSummary reports how many rows Migrate copied per table — the
// "matching row counts per table" proof Milestone 7 (HYVE-ORGANIZATION-MODEL-IMPLEMENTATION-PLAN.md,
// nexus-config/docs) calls for.
type MigrationSummary struct {
	ReconcilingClusters int
	Organizations       int
	Environments        int
	Bindings            int
	SigningKeys         int
	Sessions            int
}

// Migrate copies every row from source into dest, table by table, in FK
// dependency order (reconciling_clusters -> organizations -> environments
// -> bindings; signing_keys/sessions have no FKs) — the one-shot SQLite->
// Postgres dump/restore tool Milestone 7's own deployment-strategy
// hardening calls for (see HYVE-ORGANIZATION-MODEL-PROPOSAL.md's
// "Deployment strategy" section for why a real install eventually needs
// this: SQLite has no story for concurrent multi-process access, so
// growing past one API replica — or using Milestone 6's per-organization
// reconciling clusters at all, which assumes exactly that — means
// migrating an already-populated SQLite database into Postgres rather
// than starting over empty).
//
// Every row keeps its original id (every Create* method used here accepts
// a pre-set id and preserves it rather than generating a new one) — what
// actually matters for correctness, since ids are what every foreign key
// and every session/access token already issued to a client reference.
// created_at is the one field this deliberately does NOT preserve: every
// Create* method's own INSERT always uses the schema's own
// DEFAULT CURRENT_TIMESTAMP/now() rather than accepting a caller-supplied
// value, so a migrated row's created_at reflects when the migration ran,
// not when the row was originally created. Accepted, not fixed here: audit-
// trail fidelity for a handful of timestamp columns is a real but minor
// loss, and preserving it would mean either a second, parallel set of raw-
// insert methods per table (real duplication for a one-shot tool) or
// loosening every Create*'s own signature for every ordinary caller's
// sake, neither of which is worth it for this.
//
// Callers are responsible for the API server being down for the duration
// (see this function's own doc-comment precedent everywhere else in this
// plan for "why a lock/downtime, not a live migration") — Migrate itself
// has no locking of its own; it assumes dest is not being written to by
// anything else while it runs, and refuses to run at all against a source
// with an in-flight PATCH /organizations reconciling-cluster migration
// (reconciling_cluster_migration_status set) or a dest that isn't empty,
// both safety checks below.
func Migrate(ctx context.Context, source, dest *Store) (MigrationSummary, error) {
	var summary MigrationSummary

	if err := checkDestEmpty(ctx, dest); err != nil {
		return summary, err
	}

	reconcilingClusters, err := source.ListReconcilingClusters(ctx)
	if err != nil {
		return summary, fmt.Errorf("list source reconciling clusters: %w", err)
	}
	for _, rc := range reconcilingClusters {
		if _, err := dest.CreateReconcilingCluster(ctx, ReconcilingCluster{ID: rc.ID, Name: rc.Name, Kubeconfig: rc.Kubeconfig}); err != nil {
			return summary, fmt.Errorf("copy reconciling cluster %q: %w", rc.Name, err)
		}
		summary.ReconcilingClusters++
	}

	organizations, err := source.ListOrganizations(ctx)
	if err != nil {
		return summary, fmt.Errorf("list source organizations: %w", err)
	}
	for _, org := range organizations {
		if org.ReconcilingClusterMigrationStatus != nil {
			return summary, fmt.Errorf("organization %q has an in-flight reconciling-cluster migration (PATCH /organizations/%s) — let it finish or fail cleanly before running this database migration", org.Name, org.Name)
		}
		if _, err := dest.CreateOrganization(ctx, org); err != nil {
			return summary, fmt.Errorf("copy organization %q: %w", org.Name, err)
		}
		summary.Organizations++

		envs, err := source.ListEnvironments(ctx, org.ID)
		if err != nil {
			return summary, fmt.Errorf("list environments for organization %q: %w", org.Name, err)
		}
		for _, env := range envs {
			if _, err := dest.CreateEnvironment(ctx, env); err != nil {
				return summary, fmt.Errorf("copy environment %q/%q: %w", org.Name, env.Name, err)
			}
			summary.Environments++
		}
	}

	bindings, err := source.ListAllBindings(ctx)
	if err != nil {
		return summary, fmt.Errorf("list source bindings: %w", err)
	}
	for _, b := range bindings {
		if _, err := dest.CreateBinding(ctx, b); err != nil {
			return summary, fmt.Errorf("copy binding %q/%q: %w", b.Namespace, b.Identity, err)
		}
		summary.Bindings++
	}

	signingKeys, err := source.ListSigningKeys(ctx)
	if err != nil {
		return summary, fmt.Errorf("list source signing keys: %w", err)
	}
	for _, k := range signingKeys {
		if _, err := dest.CreateSigningKey(ctx, k); err != nil {
			return summary, fmt.Errorf("copy signing key for namespace %q: %w", k.Namespace, err)
		}
		summary.SigningKeys++
	}

	sessions, err := source.ListSessions(ctx)
	if err != nil {
		return summary, fmt.Errorf("list source sessions: %w", err)
	}
	for _, sess := range sessions {
		if _, err := dest.CreateSession(ctx, sess); err != nil {
			return summary, fmt.Errorf("copy session %q: %w", sess.ID, err)
		}
		summary.Sessions++
	}

	return summary, nil
}

// checkDestEmpty refuses to run Migrate against a destination that already
// has data — almost certainly either an already-completed migration being
// accidentally re-run (which would duplicate-key-fail partway through
// anyway, but only after copying some rows) or a DSN pointed at the wrong
// database entirely. An operator who genuinely wants to re-run this
// points it at a fresh, empty Postgres database instead.
func checkDestEmpty(ctx context.Context, dest *Store) error {
	orgs, err := dest.ListOrganizations(ctx)
	if err != nil {
		return fmt.Errorf("check destination organizations: %w", err)
	}
	if len(orgs) > 0 {
		return fmt.Errorf("destination database already has %d organization(s) — refusing to migrate into a non-empty database (point --to-dsn at a fresh, empty database)", len(orgs))
	}
	clusters, err := dest.ListReconcilingClusters(ctx)
	if err != nil {
		return fmt.Errorf("check destination reconciling clusters: %w", err)
	}
	if len(clusters) > 0 {
		return fmt.Errorf("destination database already has %d reconciling cluster(s) — refusing to migrate into a non-empty database (point --to-dsn at a fresh, empty database)", len(clusters))
	}
	return nil
}
