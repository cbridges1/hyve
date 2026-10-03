package api

import (
	"context"

	"github.com/cbridges1/hyve/internal/orgdb"
)

// findBindingBySubject looks up (subjectType, identity)'s membership within
// namespace — the actual isolation boundary (see orgdb.Binding's own doc
// comment). A user's role is per membership, so a role change takes effect
// on the very next request, not just the next login. Credentials live on
// the user, not here — see findUserForLogin.
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

// findUserForLogin finds the user a login (or password reset) identifier
// names: a username first, then an email address — both unique across the
// install. Same nil-OrgStore fail-closed behavior as findBindingBySubject.
func (s *Server) findUserForLogin(ctx context.Context, identifier string) (orgdb.User, error) {
	if s.OrgStore == nil {
		return orgdb.User{}, orgdb.ErrNotFound
	}
	u, err := s.OrgStore.GetUserByUsername(ctx, identifier)
	if err == nil {
		return u, nil
	}
	return s.OrgStore.GetUserByEmail(ctx, identifier)
}

// userEmail returns username's email, nil when they have none (or no user
// row at all).
func (s *Server) userEmail(ctx context.Context, username string) *string {
	if s.OrgStore == nil {
		return nil
	}
	u, err := s.OrgStore.GetUserByUsername(ctx, username)
	if err != nil {
		return nil
	}
	return u.Email
}

// memberElsewhere reports whether username belongs to any namespace other
// than namespace. A user's email and password are shared by all their
// organizations, so an admin of one organization may only change them for
// a user who belongs to that organization alone — otherwise they could,
// say, redirect a password reset for someone else's account.
func (s *Server) memberElsewhere(ctx context.Context, username, namespace string) (bool, error) {
	memberships, err := s.OrgStore.ListBindingsForIdentity(ctx, orgdb.SubjectTypeLocal, username)
	if err != nil {
		return false, err
	}
	for _, b := range memberships {
		if b.Namespace != namespace {
			return true, nil
		}
	}
	return false, nil
}
