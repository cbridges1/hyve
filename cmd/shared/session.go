package shared

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/cbridges1/hyve/internal/session"
)

// LoadSession returns the current global cluster-mode session (see `hyve
// login`), if any, with no refresh attempt and no validity check — the raw
// local record. Returns (nil, nil), not an error, when nothing is logged
// in at all. Most callers want EnsureValidSession instead, which silently
// refreshes an expired access token first.
func LoadSession() (*session.Session, error) {
	return session.Load()
}

// EnsureValidSession loads the current session and, if its cached access
// token has expired but the underlying session hasn't, silently exchanges
// it for a new one via POST /auth/refresh — no password, no user
// interaction, which is what makes unattended/automated use practical (see
// internal/api's AccessTokenTTL/SessionTTL doc comments).
//
// Returns (nil, nil) when nothing is logged in at all — that's not an
// error, it's "local mode is correct here." Returns (sess, err) when
// something IS logged in but couldn't be made to work (the session itself
// has expired, or the server rejected the refresh, e.g. because it was
// revoked by `hyve logout` elsewhere) — callers decide whether that's
// fatal: UseClusterMode treats it as fatal (see its own doc comment for
// why silently falling back to local files is dangerous for a cluster-mode
// environment); LoadEnvironmentSecrets treats it as safely ignorable,
// since it must never abort a command over background secret-loading.
func EnsureValidSession() (*session.Session, error) {
	sess, err := session.Load()
	if err != nil {
		return nil, err
	}
	if sess == nil {
		return nil, nil
	}
	if sess.AccessTokenValid() {
		return sess, nil
	}
	if !sess.SessionValid() {
		return sess, fmt.Errorf("session expired at %s", sess.SessionExpiresAt)
	}

	accessToken, expiresAt, err := refreshAccessToken(sess)
	if err != nil {
		return sess, fmt.Errorf("failed to refresh session: %w", err)
	}
	sess.AccessToken = accessToken
	sess.AccessTokenExpiresAt = expiresAt
	return sess, nil
}

// RevokeSession calls POST /auth/logout with sess's session token, telling
// the server to delete the underlying HyveSession — real, immediate
// revocation (see internal/api's handleLogout). Callers should still clear
// the local record (session.Clear) even if this fails: the end state the
// user actually cares about ("hyve no longer treats me as logged in on
// this machine") is achieved either way.
func RevokeSession(sess *session.Session) error {
	body, err := json.Marshal(struct {
		SessionToken string `json:"sessionToken"`
	}{SessionToken: sess.SessionToken()})
	if err != nil {
		return fmt.Errorf("marshal logout request: %w", err)
	}
	req, err := http.NewRequest(http.MethodPost, strings.TrimRight(sess.APIURL, "/")+"/auth/logout", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build logout request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("reach %s: %w", sess.APIURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected response: %s", resp.Status)
	}
	return nil
}

// refreshAccessToken exchanges sess's still-valid session token for a new
// access token via POST /auth/refresh, persists it locally, and returns
// the new token and its expiry. Deliberately bypasses APIClient.do: that
// path always sends an "Authorization: Bearer <access token>" header, but
// refresh authenticates with the session token in the body instead — the
// whole point is that it must still work once the access token has
// already expired.
func refreshAccessToken(sess *session.Session) (accessToken, expiresAt string, err error) {
	body, err := json.Marshal(struct {
		SessionToken string `json:"sessionToken"`
	}{SessionToken: sess.SessionToken()})
	if err != nil {
		return "", "", fmt.Errorf("marshal refresh request: %w", err)
	}
	req, err := http.NewRequest(http.MethodPost, strings.TrimRight(sess.APIURL, "/")+"/auth/refresh", bytes.NewReader(body))
	if err != nil {
		return "", "", fmt.Errorf("build refresh request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("reach %s: %w", sess.APIURL, err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		var apiErr struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(respBody, &apiErr) == nil && apiErr.Error != "" {
			return "", "", fmt.Errorf("%s (%s)", apiErr.Error, resp.Status)
		}
		return "", "", fmt.Errorf("unexpected response: %s", resp.Status)
	}

	var out struct {
		AccessToken          string `json:"accessToken"`
		AccessTokenExpiresAt string `json:"accessTokenExpiresAt"`
	}
	if err := json.Unmarshal(respBody, &out); err != nil {
		return "", "", fmt.Errorf("parse refresh response: %w", err)
	}
	if err := session.SaveAccessToken(out.AccessToken, out.AccessTokenExpiresAt); err != nil {
		return "", "", fmt.Errorf("save refreshed access token: %w", err)
	}
	return out.AccessToken, out.AccessTokenExpiresAt, nil
}
