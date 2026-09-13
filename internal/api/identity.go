package api

import (
	"context"

	"github.com/cbridges1/hyve/internal/orgdb"
)

// organizationIDForNamespace resolves a request-scoped namespace (from
// Server.TenantNamespace) into the *string organizationID
// orgdb.Store.FindBindingBySubject/ListBindingsForScope expect — nil means
// "no organization" scope, covering two genuinely different cases the
// caller doesn't need to (and shouldn't have to) tell apart:
//
//   - namespace == s.Namespace: the control-plane/superadmin scope,
//     matching the retired CRD-based FindBindingBySubject's own
//     "namespace == s.Namespace means the control plane" dispatch.
//   - namespace has no matching Organization at all: a self-hosted,
//     single-tenant install that never ran POST /organizations (Phase 1's
//     original one-namespace-holds-everything model — see
//     HYVE-MULTI-TENANCY-PLAN.md) — its ordinary admin/read-only bindings
//     live directly in that one namespace, exactly like a superadmin's,
//     with no Organization/Environment layer above them at all. This
//     mirrors resolveResourceEnvironment's own identical "no organization
//     for this namespace -> legacy behavior, not an error" stance for the
//     four resource types (see environmentnaming.go) — bindings get the
//     same additive, backward-compatible treatment.
//
// Only a genuine datastore failure (not "no organization found") returns a
// non-nil error. A nil s.OrgStore is treated the same as "no organization
// found" here (not an error) — see findBindingBySubject's own doc comment
// for why callers resolving a *binding* still fail closed instead, at a
// layer above this one.
func (s *Server) organizationIDForNamespace(ctx context.Context, namespace string) (*string, error) {
	if namespace == s.Namespace || s.OrgStore == nil {
		return nil, nil
	}
	org, err := s.OrgStore.GetOrganizationByName(ctx, namespace)
	if err == orgdb.ErrNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &org.ID, nil
}

// findBindingBySubject looks up (subjectType, identity)'s binding within
// namespace — the actual isolation boundary (see orgdb.Binding's own doc
// comment) — the Server-method replacement for the retired CRD-based
// package-level FindBindingBySubject(ctx, client.Client, namespace, ...),
// now with an identical namespace-scoped signature. Used at login time (to
// find the paired credentials Secret — see LoadPasswordHash) and by the
// authz middleware (to resolve a role per-request — see server.go's
// requireRole), so a role change on a binding takes effect on the very
// next request, not just the next login, exactly as before.
//
// A nil s.OrgStore (a Server built before this field existed, including
// any test not exercising binding lookups at all) fails closed with
// orgdb.ErrNotFound rather than panicking.
func (s *Server) findBindingBySubject(ctx context.Context, namespace, subjectType, identity string) (orgdb.Binding, error) {
	if s.OrgStore == nil {
		return orgdb.Binding{}, orgdb.ErrNotFound
	}
	return s.OrgStore.FindBindingBySubject(ctx, namespace, subjectType, identity)
}
