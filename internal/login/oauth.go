package login

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"golang.org/x/oauth2"
)

// OAuth flows for Azure AD, as Rancher's CLI does them
// (rancher/cli kubectl_token_oauth.go).
const (
	OAuthDeviceCode = "devicecode"
	OAuthAuthCode   = "authcode"
)

func oauthConfig(p Provider) *oauth2.Config {
	return &oauth2.Config{
		ClientID: p.ClientID,
		Scopes:   p.Scopes,
		Endpoint: oauth2.Endpoint{AuthURL: p.AuthURL, DeviceAuthURL: p.DeviceAuthURL, TokenURL: p.TokenURL},
	}
}

// OAuthLogin signs in through an OAuth provider (Azure AD): it gets an ID
// token from the identity provider, by device code (the default) or by
// auth code with a localhost callback, and trades it for a Rancher token.
func OAuthLogin(ctx context.Context, c *http.Client, base string, p Provider, flow string, open func(string) error, out io.Writer) (Session, error) {
	ctx = context.WithValue(ctx, oauth2.HTTPClient, c)
	cfg := oauthConfig(p)

	var (
		tok *oauth2.Token
		err error
	)
	switch flow {
	case "", OAuthDeviceCode:
		var da *oauth2.DeviceAuthResponse
		da, err = cfg.DeviceAuth(ctx)
		if err != nil {
			return Session{}, fmt.Errorf("starting the device sign-in: %w", err)
		}
		fmt.Fprintf(out, "To sign in, open %s and enter the code %s\n", da.VerificationURI, da.UserCode)
		tok, err = cfg.DeviceAccessToken(ctx, da)
	case OAuthAuthCode:
		tok, err = authCode(ctx, cfg, open, out)
	default:
		return Session{}, fmt.Errorf("unknown OAuth flow %q (want %s or %s)", flow, OAuthDeviceCode, OAuthAuthCode)
	}
	if err != nil {
		return Session{}, err
	}
	idToken, _ := tok.Extra("id_token").(string)
	if idToken == "" {
		return Session{}, errors.New("the identity provider returned no ID token")
	}

	resp, data, err := do(ctx, c, http.MethodPost, base+"/v1-public/login", map[string]any{
		"type":         p.Type,
		"responseType": responseType,
		"id_token":     idToken,
	}, "")
	if err != nil {
		return Session{}, err
	}
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		return Session{}, fmt.Errorf("Rancher refused the sign-in: %s", resp.Status)
	}
	return parseSession(data)
}

func authCode(ctx context.Context, cfg *oauth2.Config, open func(string) error, out io.Writer) (*oauth2.Token, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	defer ln.Close()
	cfg.RedirectURL = fmt.Sprintf("http://localhost:%d", ln.Addr().(*net.TCPAddr).Port)

	state, err := randomState()
	if err != nil {
		return nil, err
	}
	verifier := oauth2.GenerateVerifier()
	authURL := cfg.AuthCodeURL(state, oauth2.S256ChallengeOption(verifier))

	codeCh := make(chan string, 1)
	errCh := make(chan error, 1)
	srv := &http.Server{ReadHeaderTimeout: 10 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		switch {
		case q.Get("error") != "":
			http.Error(w, "Sign-in failed. You can close this window.", http.StatusBadRequest)
			trySend(errCh, fmt.Errorf("sign-in failed: %s: %s", q.Get("error"), q.Get("error_description")))
		case q.Get("state") != state:
			http.Error(w, "Invalid state.", http.StatusBadRequest)
			trySend(errCh, errors.New("the callback's state didn't match"))
		case q.Get("code") == "":
			http.Error(w, "Missing code.", http.StatusBadRequest)
			trySend(errCh, errors.New("the callback had no code"))
		default:
			fmt.Fprint(w, "Signed in. You can close this window and return to the terminal.")
			trySend(codeCh, q.Get("code"))
		}
	})}
	go func() { _ = srv.Serve(ln) }()
	defer srv.Close()

	fmt.Fprintf(out, "Sign in in your browser:\n  %s\n", authURL)
	if open != nil {
		_ = open(authURL)
	}

	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	select {
	case code := <-codeCh:
		return cfg.Exchange(ctx, code, oauth2.VerifierOption(verifier))
	case err := <-errCh:
		return nil, err
	case <-ctx.Done():
		return nil, errors.New("timed out waiting for the browser sign-in")
	}
}

func trySend[T any](ch chan T, v T) {
	select {
	case ch <- v:
	default:
	}
}

func randomState() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
