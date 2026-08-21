package shared

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"golang.org/x/term"

	"github.com/cbridges1/hyve/internal/repository"
	"github.com/cbridges1/hyve/internal/session"
)

// PerformLogin authenticates against apiURL's local (username/password)
// auth and returns the resulting session — both the short-lived access
// token (used on every /api/* request) and the longer-lived session token
// (used to silently refresh it — see internal/api's HyveSession/
// AccessTokenTTL/SessionTTL doc comments). Username is carried onto the
// returned Session purely for display (e.g. `hyve whoami`'s local-only
// summary before its own server round trip).
func PerformLogin(apiURL, username, password string) (*session.Session, error) {
	body, err := json.Marshal(map[string]string{"username": username, "password": password})
	if err != nil {
		return nil, fmt.Errorf("failed to build request: %w", err)
	}

	trimmedURL := strings.TrimRight(apiURL, "/")
	resp, err := http.Post(trimmedURL+"/auth/login", "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to reach %s: %w", trimmedURL, err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("login failed (%s): %s", resp.Status, strings.TrimSpace(string(respBody)))
	}

	var loginResp struct {
		AccessToken          string `json:"accessToken"`
		AccessTokenExpiresAt string `json:"accessTokenExpiresAt"`
		SessionToken         string `json:"sessionToken"`
		SessionExpiresAt     string `json:"sessionExpiresAt"`
	}
	if err := json.Unmarshal(respBody, &loginResp); err != nil {
		return nil, fmt.Errorf("failed to parse login response: %w", err)
	}

	sessionID, sessionSecret, ok := strings.Cut(loginResp.SessionToken, ".")
	if !ok {
		return nil, fmt.Errorf("malformed session token in login response")
	}

	return &session.Session{
		Username:             username,
		APIURL:               trimmedURL,
		SessionID:            sessionID,
		SessionSecret:        sessionSecret,
		SessionExpiresAt:     loginResp.SessionExpiresAt,
		AccessToken:          loginResp.AccessToken,
		AccessTokenExpiresAt: loginResp.AccessTokenExpiresAt,
	}, nil
}

// PromptLine prompts on stderr and reads one line from stdin.
func PromptLine(label string) (string, error) {
	fmt.Fprint(os.Stderr, label)
	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

// PromptSecret reads a line without echoing it — falls back to a plain
// (echoed) read when stdin isn't a real terminal, since term.ReadPassword
// requires an actual TTY file descriptor.
func PromptSecret(label string) (string, error) {
	fmt.Fprint(os.Stderr, label)
	if term.IsTerminal(int(os.Stdin.Fd())) {
		data, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(os.Stderr)
		if err != nil {
			return "", err
		}
		return string(data), nil
	}
	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}

// UniqueEnvironmentName returns base, or base-2, base-3, ... — whichever is
// the first name not already registered. Shared by `hyve login`'s
// auto-provisioning path and `hyve env create`'s name-omitted default.
func UniqueEnvironmentName(repoMgr *repository.Manager, base string) string {
	name := base
	for i := 2; ; i++ {
		if _, err := repoMgr.GetRepositoryByName(name); err != nil {
			return name
		}
		name = fmt.Sprintf("%s-%d", base, i)
	}
}
