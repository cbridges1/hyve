package api

import (
	"context"

	"github.com/cbridges1/hyve/internal/orgdb"
)

// findBindingBySubject looks up (subjectType, identity)'s binding within
// namespace — the actual isolation boundary (see orgdb.Binding's own doc
// comment) — the Server-method replacement for the retired CRD-based
// package-level FindBindingBySubject(ctx, client.Client, namespace, ...),
// now with an identical namespace-scoped signature. Used at login time (to
// find the paired password hash — see Binding.PasswordHash) and by the
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

// findBindingByEmail is findBindingBySubject's email-lookup counterpart —
// same nil-OrgStore fail-closed behavior, used by handleLogin's
// email-as-identifier fallback.
func (s *Server) findBindingByEmail(ctx context.Context, namespace, email string) (orgdb.Binding, error) {
	if s.OrgStore == nil {
		return orgdb.Binding{}, orgdb.ErrNotFound
	}
	return s.OrgStore.FindBindingByEmail(ctx, namespace, email)
}
