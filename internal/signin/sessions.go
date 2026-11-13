package signin

import (
	"context"
	"errors"
	"log"
	"net/http"
	"sort"
	"time"

	authorizationv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	hyvev1alpha1 "github.com/cbridges1/hyve/internal/apis/hyve/v1alpha1"
)

// Where a session was started, kept on its Secret so a user can tell
// their sessions apart (and sign out the ones they don't know).
const (
	annotationUserAgent = "hyve.io/user-agent"
	annotationAddress   = "hyve.io/address"
	maxUserAgent        = 256
)

// ErrNoSession is a session that isn't there (or isn't the user's).
var ErrNoSession = errors.New("no such session")

// SessionInfo describes a signed-in session (a browser's or a CLI's, or a
// CI job's); API tokens are listed with ListTokens.
type SessionInfo struct {
	User      string     `json:"user"`
	ID        string     `json:"id"`
	Kind      string     `json:"kind"`
	UserAgent string     `json:"userAgent,omitempty"`
	Address   string     `json:"address,omitempty"`
	CreatedAt time.Time  `json:"createdAt"`
	ExpiresAt time.Time  `json:"expiresAt"`
	LastUsed  *time.Time `json:"lastUsed,omitempty"`
	// Current is the session the request came with.
	Current bool `json:"current,omitempty"`
}

type clientKey struct{}

type clientInfo struct{ userAgent, address string }

// withClient keeps where a request came from in its context, for the
// sessions it starts.
func withClient(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ua := r.UserAgent()
		if len(ua) > maxUserAgent {
			ua = ua[:maxUserAgent]
		}
		ctx := context.WithValue(r.Context(), clientKey{}, clientInfo{userAgent: ua, address: clientAddr(r)})
		h.ServeHTTP(w, r.WithContext(ctx))
	})
}

// annotateClient adds the context's client to a new session's
// annotations.
func annotateClient(ctx context.Context, ann map[string]string) map[string]string {
	ci, ok := ctx.Value(clientKey{}).(clientInfo)
	if !ok {
		return ann
	}
	out := map[string]string{}
	for k, v := range ann {
		out[k] = v
	}
	if ci.userAgent != "" {
		out[annotationUserAgent] = ci.userAgent
	}
	if ci.address != "" {
		out[annotationAddress] = ci.address
	}
	return out
}

// ListSessions returns user's sessions that haven't expired, newest
// first.
func (s *Store) ListSessions(ctx context.Context, user string) ([]SessionInfo, error) {
	var list corev1.SecretList
	if err := s.Client.List(ctx, &list, client.InNamespace(s.Namespace), client.MatchingLabels{labelSessionFor: user}); err != nil {
		return nil, err
	}
	now := s.now()
	out := []SessionInfo{}
	for i := range list.Items {
		sec := &list.Items[i]
		if sessionKind(sec) == KindToken {
			continue
		}
		info := SessionInfo{
			User:      user,
			ID:        sec.Name[len(sessionSecretName("")):],
			Kind:      sessionKind(sec),
			UserAgent: sec.Annotations[annotationUserAgent],
			Address:   sec.Annotations[annotationAddress],
			CreatedAt: sec.CreationTimestamp.UTC(),
		}
		if info.Kind == KindSession {
			info.Kind = "session"
		}
		info.ExpiresAt, _ = time.Parse(time.RFC3339, string(sec.Data[keyExpiresAt]))
		if !now.Before(info.ExpiresAt) {
			continue
		}
		if lu, err := time.Parse(time.RFC3339, sec.Annotations[annotationLastUsed]); err == nil {
			info.LastUsed = &lu
		}
		out = append(out, info)
	}
	sort.Slice(out, func(a, b int) bool { return out[a].CreatedAt.After(out[b].CreatedAt) })
	return out, nil
}

// RevokeSession signs out user's session id. An API token isn't one.
func (s *Store) RevokeSession(ctx context.Context, user, id string) error {
	var sec corev1.Secret
	err := s.Client.Get(ctx, client.ObjectKey{Namespace: s.Namespace, Name: sessionSecretName(id)}, &sec)
	if apierrors.IsNotFound(err) {
		return ErrNoSession
	}
	if err != nil {
		return err
	}
	if sec.Labels[labelSessionFor] != user || sessionKind(&sec) == KindToken {
		return ErrNoSession
	}
	s.forget(string(sec.Data[keyHash]))
	return client.IgnoreNotFound(s.Client.Delete(ctx, &sec))
}

// canSessions checks whether ident may verb (list, delete) user's
// sessions: their own, or with that right on the users/sessions
// subresource (hyve-user-admin).
func (s *Server) canSessions(ctx context.Context, ident Identity, verb, user string) (bool, error) {
	if user == ident.Username {
		return true, nil
	}
	sar := &authorizationv1.SubjectAccessReview{
		Spec: authorizationv1.SubjectAccessReviewSpec{
			User:   ident.KubeUser(),
			Groups: ident.KubeGroups(),
			ResourceAttributes: &authorizationv1.ResourceAttributes{
				Group:       hyvev1alpha1.GroupVersion.Group,
				Version:     hyvev1alpha1.GroupVersion.Version,
				Resource:    "users",
				Subresource: "sessions",
				Verb:        verb,
				Name:        user,
			},
		},
	}
	if err := s.Store.Client.Create(ctx, sar); err != nil {
		return false, err
	}
	return sar.Status.Allowed, nil
}

func (s *Server) allowSessions(w http.ResponseWriter, r *http.Request, ident Identity, verb, user string) bool {
	ok, err := s.canSessions(r.Context(), ident, verb, user)
	if err != nil {
		log.Printf("signin: access review for %s's sessions (%s by %s): %v", user, verb, ident.Username, err)
		writeError(w, http.StatusInternalServerError, "ServerError", "couldn't check permissions")
		return false
	}
	if !ok {
		writeError(w, http.StatusForbidden, "Forbidden", "you can't manage this user's sessions")
		return false
	}
	return true
}

// handleListSessions is GET /v1/sessions: the caller's, or ?user=NAME's.
func (s *Server) handleListSessions(w http.ResponseWriter, r *http.Request) {
	ident, ok := s.authenticate(w, r)
	if !ok {
		return
	}
	user := r.URL.Query().Get("user")
	if user == "" {
		user = ident.Username
	}
	if !s.allowSessions(w, r, ident, "list", user) {
		return
	}
	list, err := s.Store.ListSessions(r.Context(), user)
	if err != nil {
		log.Printf("signin: listing sessions: %v", err)
		writeError(w, http.StatusInternalServerError, "ServerError", "couldn't list sessions")
		return
	}
	for i := range list {
		list[i].Current = user == ident.Username && list[i].ID == ident.SessionID
	}
	writeJSON(w, http.StatusOK, list)
}

// handleRevokeSession is DELETE /v1/sessions/{user}/{id}.
func (s *Server) handleRevokeSession(w http.ResponseWriter, r *http.Request) {
	ident, ok := s.authenticate(w, r)
	if !ok {
		return
	}
	user, id := r.PathValue("user"), r.PathValue("id")
	if !s.allowSessions(w, r, ident, "delete", user) {
		return
	}
	switch err := s.Store.RevokeSession(r.Context(), user, id); {
	case errors.Is(err, ErrNoSession):
		writeError(w, http.StatusNotFound, "NotFound", err.Error())
		return
	case err != nil:
		log.Printf("signin: signing out %s's session %s: %v", user, id, err)
		writeError(w, http.StatusInternalServerError, "ServerError", "couldn't sign the session out")
		return
	}
	log.Printf("signin: %s signed out %s's session %s", ident.Username, user, id)
	if user == ident.Username && id == ident.SessionID {
		clearSessionCookie(w, r)
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleRevokeSessions is DELETE /v1/sessions/{user}: every session of
// user's but the caller's own (API tokens stay).
func (s *Server) handleRevokeSessions(w http.ResponseWriter, r *http.Request) {
	ident, ok := s.authenticate(w, r)
	if !ok {
		return
	}
	user := r.PathValue("user")
	if !s.allowSessions(w, r, ident, "delete", user) {
		return
	}
	keep := ""
	if user == ident.Username {
		keep = ident.SessionID
	}
	if err := s.Store.RevokeSessions(r.Context(), user, keep); err != nil {
		log.Printf("signin: signing out %s's sessions: %v", user, err)
		writeError(w, http.StatusInternalServerError, "ServerError", "couldn't sign the sessions out")
		return
	}
	log.Printf("signin: %s signed out %s's other sessions", ident.Username, user)
	w.WriteHeader(http.StatusNoContent)
}
