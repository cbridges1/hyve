package orgdb

import (
	"context"
	"database/sql"
	"path"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testKubeconfig = "apiVersion: v1\nkind: Config\n"

func testReconcilingClusterOwnership(t *testing.T, s *Store) {
	ctx := context.Background()

	acme, err := s.CreateOrganization(ctx, Organization{Name: "acme", Namespace: "acme"})
	require.NoError(t, err)
	globex, err := s.CreateOrganization(ctx, Organization{Name: "globex", Namespace: "globex"})
	require.NoError(t, err)

	// The same name is fine across owners — two organizations and the pool
	// can each have their own "prod".
	acmeProd, err := s.CreateReconcilingCluster(ctx, ReconcilingCluster{Name: "prod", OrganizationID: &acme.ID, Kubeconfig: testKubeconfig})
	require.NoError(t, err)
	_, err = s.CreateReconcilingCluster(ctx, ReconcilingCluster{Name: "prod", OrganizationID: &globex.ID, Kubeconfig: testKubeconfig})
	require.NoError(t, err)
	poolProd, err := s.CreateReconcilingCluster(ctx, ReconcilingCluster{Name: "prod", Kubeconfig: testKubeconfig})
	require.NoError(t, err)

	// ...but never twice within one owner.
	_, err = s.CreateReconcilingCluster(ctx, ReconcilingCluster{Name: "prod", OrganizationID: &acme.ID, Kubeconfig: testKubeconfig})
	assert.Error(t, err, "duplicate name within one organization")
	_, err = s.CreateReconcilingCluster(ctx, ReconcilingCluster{Name: "prod", Kubeconfig: testKubeconfig})
	assert.Error(t, err, "duplicate name within the pool")

	got, err := s.GetOrgReconcilingClusterByName(ctx, acme.ID, "prod")
	require.NoError(t, err)
	assert.Equal(t, acmeProd.ID, got.ID)
	require.NotNil(t, got.OrganizationID)
	assert.Equal(t, acme.ID, *got.OrganizationID)

	got, err = s.GetPoolReconcilingClusterByName(ctx, "prod")
	require.NoError(t, err)
	assert.Equal(t, poolProd.ID, got.ID, "a pool lookup must never resolve to an organization-owned row")
	assert.Nil(t, got.OrganizationID)

	staging, err := s.CreateReconcilingCluster(ctx, ReconcilingCluster{Name: "staging", OrganizationID: &acme.ID, Kubeconfig: testKubeconfig})
	require.NoError(t, err)

	owned, err := s.ListOrgReconcilingClusters(ctx, acme.ID)
	require.NoError(t, err)
	require.Len(t, owned, 2)
	assert.Equal(t, []string{"prod", "staging"}, []string{owned[0].Name, owned[1].Name})

	all, err := s.ListReconcilingClusters(ctx)
	require.NoError(t, err)
	assert.Len(t, all, 4, "the health sweep still sees every cluster, owned or pooled")

	// Deleting an organization takes its own clusters with it, even the one
	// it's currently assigned to — and leaves everyone else's alone.
	require.NoError(t, s.SetOrganizationReconcilingCluster(ctx, acme.ID, &acmeProd.ID))
	require.NoError(t, s.DeleteOrganization(ctx, acme.ID))
	_, err = s.GetReconcilingCluster(ctx, acmeProd.ID)
	assert.ErrorIs(t, err, ErrNotFound)
	_, err = s.GetReconcilingCluster(ctx, staging.ID)
	assert.ErrorIs(t, err, ErrNotFound)
	_, err = s.GetOrgReconcilingClusterByName(ctx, globex.ID, "prod")
	assert.NoError(t, err)
	_, err = s.GetReconcilingCluster(ctx, poolProd.ID)
	assert.NoError(t, err)
}

func TestReconcilingClusterOwnership_SQLite(t *testing.T) {
	testReconcilingClusterOwnership(t, openTestSQLite(t))
}

func TestReconcilingClusterOwnership_Postgres(t *testing.T) {
	testReconcilingClusterOwnership(t, openTestPostgres(t))
}

// TestMigration0006_BackfillsOwnership builds a database at the schema as it
// stood before 0006 (reconciling cluster names globally unique, the
// one-cluster-per-organization convention of naming it after the
// namespace), then opens it normally so 0006 runs against real data.
func TestMigration0006_BackfillsOwnership(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "orgdb.sqlite")
	db, err := sql.Open("sqlite", dbPath)
	require.NoError(t, err)

	_, err = db.Exec(`CREATE TABLE schema_migrations (version TEXT PRIMARY KEY, applied_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP)`)
	require.NoError(t, err)
	entries, err := sqliteMigrations.ReadDir("migrations/sqlite")
	require.NoError(t, err)
	for _, e := range entries {
		version := strings.TrimSuffix(e.Name(), ".sql")
		if version >= "0006" {
			continue
		}
		content, err := sqliteMigrations.ReadFile(path.Join("migrations/sqlite", e.Name()))
		require.NoError(t, err)
		for _, stmt := range splitStatements(string(content)) {
			_, err := db.Exec(stmt)
			require.NoError(t, err, "applying %s", e.Name())
		}
		_, err = db.Exec(`INSERT INTO schema_migrations (version) VALUES (?)`, version)
		require.NoError(t, err)
	}

	for _, stmt := range []string{
		`INSERT INTO reconciling_clusters (id, name, kubeconfig) VALUES ('rc-own', 'acme', 'kc-own')`,
		`INSERT INTO reconciling_clusters (id, name, kubeconfig, kubernetes_version) VALUES ('rc-pool', 'cell-a', 'kc-pool', 'v1.31.0')`,
		`INSERT INTO organizations (id, name, namespace, reconciling_cluster_id) VALUES ('org-acme', 'acme', 'acme', 'rc-own')`,
		`INSERT INTO organizations (id, name, namespace, reconciling_cluster_id) VALUES ('org-globex', 'globex', 'globex', 'rc-pool')`,
	} {
		_, err := db.Exec(stmt)
		require.NoError(t, err)
	}
	require.NoError(t, db.Close())

	s, err := Open("sqlite", dbPath)
	require.NoError(t, err)
	t.Cleanup(func() { s.Close() })
	ctx := context.Background()

	own, err := s.GetReconcilingCluster(ctx, "rc-own")
	require.NoError(t, err)
	require.NotNil(t, own.OrganizationID, "a cluster named after an organization's namespace was that organization's own")
	assert.Equal(t, "org-acme", *own.OrganizationID)
	assert.Equal(t, "kc-own", own.Kubeconfig)

	pool, err := s.GetPoolReconcilingClusterByName(ctx, "cell-a")
	require.NoError(t, err)
	assert.Equal(t, "rc-pool", pool.ID)
	require.NotNil(t, pool.KubernetesVersion)
	assert.Equal(t, "v1.31.0", *pool.KubernetesVersion, "existing columns survive the rebuild")

	acme, err := s.GetOrganization(ctx, "org-acme")
	require.NoError(t, err)
	require.NotNil(t, acme.ReconcilingClusterID)
	assert.Equal(t, "rc-own", *acme.ReconcilingClusterID, "organizations' references survive the rebuild")

	// Per-owner uniqueness is in force after the rebuild.
	_, err = s.CreateReconcilingCluster(ctx, ReconcilingCluster{Name: "acme", Kubeconfig: testKubeconfig})
	assert.NoError(t, err, "the pool can now reuse a name an organization's own cluster has")
	_, err = s.CreateReconcilingCluster(ctx, ReconcilingCluster{Name: "cell-a", Kubeconfig: testKubeconfig})
	assert.Error(t, err)

	// Foreign keys are still enforced on ordinary connections afterwards.
	_, err = s.CreateReconcilingCluster(ctx, ReconcilingCluster{Name: "x", OrganizationID: ptr("no-such-org"), Kubeconfig: testKubeconfig})
	assert.Error(t, err)
}
