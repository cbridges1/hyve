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
)

// PerformLogin authenticates against apiURL's local (username/password)
// auth and returns the resulting token/expiry — shared by `hyve login` and
// `hyve env create --api-url`, since both need to make the exact same
// request.
func PerformLogin(apiURL, username, password string) (token, expiresAt string, err error) {
	body, err := json.Marshal(map[string]string{"username": username, "password": password})
	if err != nil {
		return "", "", fmt.Errorf("failed to build request: %w", err)
	}

	trimmedURL := strings.TrimRight(apiURL, "/")
	resp, err := http.Post(trimmedURL+"/auth/login", "application/json", bytes.NewReader(body))
	if err != nil {
		return "", "", fmt.Errorf("failed to reach %s: %w", trimmedURL, err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("login failed (%s): %s", resp.Status, strings.TrimSpace(string(respBody)))
	}

	var loginResp struct {
		Token     string `json:"token"`
		ExpiresAt string `json:"expiresAt"`
	}
	if err := json.Unmarshal(respBody, &loginResp); err != nil {
		return "", "", fmt.Errorf("failed to parse login response: %w", err)
	}

	return loginResp.Token, loginResp.ExpiresAt, nil
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
