package shared

import (
	"time"

	"github.com/cbridges1/hyve/internal/repository"
)

// Session is the cluster-mode credential `hyve login` attaches to the
// currently-active environment (see internal/repository, cmd/env) —
// entirely separate from any Kubernetes kubeconfig. EnvironmentName is
// carried along purely for display (e.g. `hyve whoami`); it plays no part
// in authentication itself.
type Session struct {
	EnvironmentName string
	APIURL          string
	Token           string
	ExpiresAt       string
}

// LoadSession reads the currently-active environment and returns its
// attached session, if any. Returns (nil, nil) — not an error — both when
// no environment is registered at all and when the active one has never
// logged in, so callers can distinguish "not logged in" (fall back to local
// mode) from a genuine lookup failure (surface it). Does not contact the
// API server or verify the token is still accepted server-side — see
// cmd's `hyve whoami` for that; this is the cheap, local-only check
// cmd/cluster's mode-detection uses.
func LoadSession() (*Session, error) {
	repoMgr, err := repository.NewManager()
	if err != nil {
		return nil, err
	}
	defer repoMgr.Close()

	current, err := repoMgr.GetCurrentRepository()
	if err != nil {
		// No environment registered yet at all — not an error, just "not
		// logged in" from the caller's perspective.
		return nil, nil
	}
	if !current.LoggedIn() {
		return nil, nil
	}

	return &Session{
		EnvironmentName: current.Name,
		APIURL:          current.APIURL,
		Token:           current.SessionToken,
		ExpiresAt:       current.SessionExpiresAt,
	}, nil
}

// Valid reports whether the session hasn't expired yet, per its own local
// record (no server round trip — see LoadSession's doc comment).
func (s *Session) Valid() bool {
	if s == nil {
		return false
	}
	expiresAt, err := time.Parse(time.RFC3339, s.ExpiresAt)
	if err != nil {
		return false
	}
	return time.Now().Before(expiresAt)
}
