package login

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"time"
)

// SAMLOptions tunes SAMLLogin; zero values match Rancher's CLI.
type SAMLOptions struct {
	PollInterval time.Duration // default 10s
	Timeout      time.Duration // default 15m
}

// SAMLLogin signs in to Rancher through a SAML provider the way Rancher's
// CLI does (rancher/cli samlAuth): it makes an RSA key pair and a request
// ID, sends the user to Rancher's sign-in page with both, and polls
// /v1-public/authtokens/<id> until Rancher has stored the new token,
// encrypted with the public key. The token is decrypted here and deleted
// on the server.
func SAMLLogin(ctx context.Context, c *http.Client, base string, open func(string) error, out io.Writer, opts SAMLOptions) (Session, error) {
	if opts.PollInterval == 0 {
		opts.PollInterval = 10 * time.Second
	}
	if opts.Timeout == 0 {
		opts.Timeout = 15 * time.Minute
	}

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return Session{}, err
	}
	// Rancher expects the public key as base64 of Go's JSON encoding of
	// rsa.PublicKey.
	pub, err := json.Marshal(key.PublicKey)
	if err != nil {
		return Session{}, err
	}
	id, err := requestID()
	if err != nil {
		return Session{}, err
	}

	u, err := url.Parse(base + "/dashboard/auth/login")
	if err != nil {
		return Session{}, err
	}
	q := url.Values{}
	q.Set("cli", "true")
	q.Set("requestId", id)
	q.Set("publicKey", base64.StdEncoding.EncodeToString(pub))
	q.Set("responseType", responseType)
	u.RawQuery = q.Encode()

	fmt.Fprintf(out, "Sign in to Rancher in your browser:\n  %s\n", u)
	if open != nil {
		if err := open(u.String()); err != nil {
			fmt.Fprintln(out, "(couldn't open a browser; open the link yourself)")
		}
	}

	ctx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()
	tokenURL := base + "/v1-public/authtokens/" + id
	tick := time.NewTicker(opts.PollInterval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return Session{}, errors.New("timed out waiting for the browser sign-in")
		case <-tick.C:
		}
		resp, body, err := do(ctx, c, http.MethodGet, tokenURL, nil, "")
		if err != nil {
			if ctx.Err() != nil {
				continue
			}
			return Session{}, err
		}
		switch resp.StatusCode {
		case http.StatusNotFound:
			continue
		case http.StatusOK:
		default:
			return Session{}, fmt.Errorf("waiting for the sign-in: %s", resp.Status)
		}
		var t struct {
			Token     string `json:"token"`
			ExpiresAt string `json:"expiresAt"`
			UserID    string `json:"userId"`
		}
		if err := json.Unmarshal(body, &t); err != nil {
			return Session{}, err
		}
		if t.Token == "" {
			continue
		}
		ciphertext, err := base64.StdEncoding.DecodeString(t.Token)
		if err != nil {
			return Session{}, fmt.Errorf("decoding the token: %w", err)
		}
		plain, err := key.Decrypt(nil, ciphertext, &rsa.OAEPOptions{Hash: crypto.SHA256})
		if err != nil {
			return Session{}, fmt.Errorf("decrypting the token: %w", err)
		}
		_, _, _ = do(context.Background(), c, http.MethodDelete, tokenURL, nil, "")

		s := Session{Token: string(plain), UserID: t.UserID}
		if t.ExpiresAt != "" {
			if ts, err := time.Parse(time.RFC3339, t.ExpiresAt); err == nil {
				s.ExpiresAt = ts
			}
		}
		return s, nil
	}
}

// requestID matches Rancher's CLI: 32 characters from a reduced alphabet.
func requestID() (string, error) {
	const alphabet = "abcdfghjklmnpqrstvwxz12456789"
	b := make([]byte, 32)
	for i := range b {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(alphabet))))
		if err != nil {
			return "", err
		}
		b[i] = alphabet[n.Int64()]
	}
	return string(b), nil
}
