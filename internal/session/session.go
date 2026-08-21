// Package session manages the CLI's single, machine-wide cluster-mode
// login — deliberately independent of internal/repository's environment
// registry. A local directory (an "environment," see cmd/env) and a
// cluster-mode session used to be the same database row, conflated as one
// concept; they're two unrelated things (most cluster-mode commands never
// touch a local directory at all — see cmd/cluster/crud.go's API branch)
// and are now stored, and selected, completely independently. `hyve login`
// authenticates once for the whole machine, the same way `gh auth login`/
// `docker login`/`aws sso login` aren't scoped to whichever project
// directory you happen to be in.
package session

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/cbridges1/hyve/internal/database"
)

// Session is the CLI's locally-cached record of a `hyve login` — both
// halves of what POST /auth/login (or /auth/refresh) returns. SessionID/
// SessionSecret together are the long-lived, revocable credential
// (internal/api's HyveSession object, identified by SessionID, verified
// against SessionSecret's hash) used to silently mint a new AccessToken
// once the cached one expires, without the user re-entering a password —
// see AccessTokenValid/SessionValid and cmd/shared's UseClusterMode.
type Session struct {
	Username             string
	APIURL               string
	SessionID            string
	SessionSecret        string
	SessionExpiresAt     string // RFC3339
	AccessToken          string
	AccessTokenExpiresAt string // RFC3339
}

// SessionToken reconstructs the "<id>.<secret>" form the API expects on
// POST /auth/refresh and POST /auth/logout.
func (s *Session) SessionToken() string {
	return s.SessionID + "." + s.SessionSecret
}

// AccessTokenValid reports whether the cached access token hasn't expired
// yet, per its own local record (no server round trip).
func (s *Session) AccessTokenValid() bool {
	return s != nil && parseTime(s.AccessTokenExpiresAt).After(time.Now())
}

// SessionValid reports whether the underlying session (the long-lived
// credential refresh depends on) hasn't expired yet.
func (s *Session) SessionValid() bool {
	return s != nil && parseTime(s.SessionExpiresAt).After(time.Now())
}

func parseTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

// Save persists sess as the current (and only) session, replacing any
// previous one — called after a successful login or refresh.
func Save(sess *Session) error {
	db, err := database.GetDB()
	if err != nil {
		return fmt.Errorf("failed to get database: %w", err)
	}
	_, err = db.Conn().Exec(`
		INSERT INTO session (id, username, api_url, session_id, session_secret, session_expires_at, access_token, access_token_expires_at, updated_at)
		VALUES (1, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(id) DO UPDATE SET
			username = excluded.username,
			api_url = excluded.api_url,
			session_id = excluded.session_id,
			session_secret = excluded.session_secret,
			session_expires_at = excluded.session_expires_at,
			access_token = excluded.access_token,
			access_token_expires_at = excluded.access_token_expires_at,
			updated_at = CURRENT_TIMESTAMP
	`, sess.Username, sess.APIURL, sess.SessionID, sess.SessionSecret, sess.SessionExpiresAt, sess.AccessToken, sess.AccessTokenExpiresAt)
	if err != nil {
		return fmt.Errorf("failed to save session: %w", err)
	}
	return nil
}

// SaveAccessToken updates just the cached access token half of the
// existing session — called after a successful silent refresh, which never
// reissues the session id/secret/expiry themselves (see
// internal/api/auth_handlers.go's handleRefresh).
func SaveAccessToken(accessToken, expiresAt string) error {
	db, err := database.GetDB()
	if err != nil {
		return fmt.Errorf("failed to get database: %w", err)
	}
	result, err := db.Conn().Exec(`UPDATE session SET access_token = ?, access_token_expires_at = ?, updated_at = CURRENT_TIMESTAMP WHERE id = 1`, accessToken, expiresAt)
	if err != nil {
		return fmt.Errorf("failed to update access token: %w", err)
	}
	if rows, _ := result.RowsAffected(); rows == 0 {
		return fmt.Errorf("no active session to refresh")
	}
	return nil
}

// Load returns the current session, or (nil, nil) if none exists — "not
// logged in" is not an error, callers distinguish it from a genuine lookup
// failure the same way internal/repository's own accessors do.
func Load() (*Session, error) {
	db, err := database.GetDB()
	if err != nil {
		return nil, fmt.Errorf("failed to get database: %w", err)
	}
	var sess Session
	err = db.Conn().QueryRow(`
		SELECT username, api_url, session_id, session_secret, session_expires_at, access_token, access_token_expires_at
		FROM session WHERE id = 1
	`).Scan(&sess.Username, &sess.APIURL, &sess.SessionID, &sess.SessionSecret, &sess.SessionExpiresAt, &sess.AccessToken, &sess.AccessTokenExpiresAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to load session: %w", err)
	}
	return &sess, nil
}

// Clear removes the current session — called by `hyve logout`, after (or
// regardless of) the server-side revocation call, so a local record never
// outlives a failed revocation attempt and silently keeps "working" from
// the CLI's own point of view.
func Clear() error {
	db, err := database.GetDB()
	if err != nil {
		return fmt.Errorf("failed to get database: %w", err)
	}
	if _, err := db.Conn().Exec(`DELETE FROM session WHERE id = 1`); err != nil {
		return fmt.Errorf("failed to clear session: %w", err)
	}
	return nil
}
