package login

import (
	"context"
	"encoding/json"
	"net/http"
	neturl "net/url"
	"time"
)

// SessionInfo is a signed-in session as the host lists it.
type SessionInfo struct {
	User      string     `json:"user"`
	ID        string     `json:"id"`
	Kind      string     `json:"kind"`
	UserAgent string     `json:"userAgent,omitempty"`
	Address   string     `json:"address,omitempty"`
	CreatedAt time.Time  `json:"createdAt"`
	ExpiresAt time.Time  `json:"expiresAt"`
	LastUsed  *time.Time `json:"lastUsed,omitempty"`
	Current   bool       `json:"current,omitempty"`
}

// ListSessions lists user's sessions ("" is yours).
func ListSessions(ctx context.Context, c *http.Client, s *Server, user string) ([]SessionInfo, error) {
	q := neturl.Values{}
	if user != "" {
		q.Set("user", user)
	}
	resp, data, err := do(ctx, c, http.MethodGet, s.URL+"/v1/sessions?"+q.Encode(), nil, s.Token)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, hostError("listing sessions", resp, data)
	}
	var out []SessionInfo
	return out, json.Unmarshal(data, &out)
}

// RevokeSession signs out user's session id; id "" signs out all of
// user's sessions but the caller's own.
func RevokeSession(ctx context.Context, c *http.Client, s *Server, user, id string) error {
	u := s.URL + "/v1/sessions/" + neturl.PathEscape(user)
	if id != "" {
		u += "/" + neturl.PathEscape(id)
	}
	resp, data, err := do(ctx, c, http.MethodDelete, u, nil, s.Token)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusNoContent {
		return hostError("signing sessions out", resp, data)
	}
	return nil
}
