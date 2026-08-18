package shared

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Session is what `hyve login` writes to <HyveHome>/session.json — the
// CLI's own cluster-mode credential, entirely separate from any Kubernetes
// kubeconfig. Lives here, not in the top-level cmd package (where `hyve
// login` itself is implemented), specifically so cmd/cluster can read it
// too without importing cmd, which would be a cycle.
type Session struct {
	APIURL    string `json:"apiUrl"`
	Token     string `json:"token"`
	ExpiresAt string `json:"expiresAt"`
}

// SessionPath returns where `hyve login` stores the current session.
func SessionPath() string {
	return filepath.Join(HyveHome(), "session.json")
}

// LoadSession reads and parses the local session file. Returns
// (nil, nil) — not an error — when no session file exists at all, so
// callers can distinguish "not logged in" (fall back to local mode) from a
// genuine read/parse failure (surface it). Does not contact the API server
// or verify the token is still accepted server-side — see cmd's `hyve
// whoami` for that; this is the cheap, local-only check cmd/cluster's
// mode-detection uses.
func LoadSession() (*Session, error) {
	data, err := os.ReadFile(SessionPath())
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", SessionPath(), err)
	}
	var sess Session
	if err := json.Unmarshal(data, &sess); err != nil {
		return nil, fmt.Errorf("parse %s: %w", SessionPath(), err)
	}
	return &sess, nil
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
