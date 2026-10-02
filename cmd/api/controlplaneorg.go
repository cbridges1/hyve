package api

import (
	"context"
	"fmt"

	"github.com/cbridges1/hyve/internal/orgdb"
)

// ensureControlPlaneOrganization is Milestone 10 Part A
// (HYVE-ORGANIZATION-MODEL-IMPLEMENTATION-PLAN.md, nexus-config/docs): the
// control plane's own namespace gets a real Organization row too, seeded
// here at startup rather than created through POST /organizations —
// internal/api's own validateOrganizationName still refuses to let a
// caller create one by this name, so this is the only place that row is
// ever written. Returns seeded=true only the first time it actually
// creates the row; a re-run against an already-seeded install is a
// no-op, matching every other idempotent startup/provisioning step in
// this codebase.
//
// No admin binding is seeded alongside it (unlike a tenant's own
// CreateOrganizationWithDefaults call, which optionally does) —
// superadmin accounts stay org-less by design (see RoleSuperadmin's own
// doc comment), created the same way they always have been, via `hyve
// cluster-config api create-user`.
//
// This one row is what makes EnvironmentsPage, environment-scoped
// resource naming, and PATCH /organizations/{name} all start working
// uniformly for the control plane's own resources too, with no
// namespace-specific special-casing left anywhere downstream — see Part
// B's own removal of the `namespace == s.Namespace` fallback this row
// makes unnecessary.
func ensureControlPlaneOrganization(ctx context.Context, store *orgdb.Store, namespace string) (seeded bool, err error) {
	_, err = store.GetOrganizationByName(ctx, namespace)
	if err == nil {
		return false, nil
	}
	if err != orgdb.ErrNotFound {
		return false, fmt.Errorf("check for control-plane organization: %w", err)
	}
	if _, _, err := store.CreateOrganizationWithDefaults(ctx, orgdb.Organization{Name: namespace, Namespace: namespace}, "", ""); err != nil {
		return false, fmt.Errorf("create control-plane organization: %w", err)
	}
	return true, nil
}
