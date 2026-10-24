package login

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"runtime"
	"strconv"
	"time"
)

// CLILoginPath is the host's CLI sign-in page (the UI's /cli-login).
//
// The page contract:
//
//   - Query: port (the CLI's localhost port), state (opaque, echoed back),
//     publicKey (base64url of an RSA SPKI DER public key, usable with
//     WebCrypto's importKey("spki", …, {name: "RSA-OAEP", hash: "SHA-256"}))
//     and optionally username (to fill in).
//   - The user signs in there as anywhere in the UI (a password, or the
//     identity provider) and confirms. The page then asks the host for a
//     new session for the CLI (POST /v1/cli-sessions), encrypts it with
//     RSA-OAEP/SHA-256, and redirects to
//     http://127.0.0.1:<port>/callback?state=…&token=<base64url>&expiresAt=<RFC 3339>,
//     or with error=<reason> if the user cancels.
const CLILoginPath = "/cli-login"

// BrowserOptions tunes BrowserLogin.
type BrowserOptions struct {
	Timeout  time.Duration // default 5m
	Username string        // filled in on the page
}

// BrowserLogin signs in through the host's CLI sign-in page, the same
// sign-in the UI uses, so the CLI never handles a password and single
// sign-in works the same way.
func BrowserLogin(ctx context.Context, base string, open func(string) error, out io.Writer, opts BrowserOptions) (Session, error) {
	if opts.Timeout == 0 {
		opts.Timeout = 5 * time.Minute
	}
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return Session{}, err
	}
	spki, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		return Session{}, err
	}
	state, err := randomState()
	if err != nil {
		return Session{}, err
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return Session{}, err
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port

	type result struct {
		s   Session
		err error
	}
	resCh := make(chan result, 1)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /callback", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("state") != state {
			http.Error(w, "Invalid state.", http.StatusBadRequest)
			return // ignore stray requests; keep waiting for the real one
		}
		if e := q.Get("error"); e != "" {
			fmt.Fprint(w, "Sign-in was cancelled. You can close this window.")
			trySend(resCh, result{err: fmt.Errorf("sign-in cancelled: %s", e)})
			return
		}
		ciphertext, err := base64.RawURLEncoding.DecodeString(q.Get("token"))
		if err != nil {
			http.Error(w, "Malformed token.", http.StatusBadRequest)
			trySend(resCh, result{err: fmt.Errorf("decoding the token: %w", err)})
			return
		}
		plain, err := key.Decrypt(nil, ciphertext, &rsa.OAEPOptions{Hash: crypto.SHA256})
		if err != nil {
			http.Error(w, "The token couldn't be decrypted.", http.StatusBadRequest)
			trySend(resCh, result{err: fmt.Errorf("decrypting the token: %w", err)})
			return
		}
		s := Session{Token: string(plain)}
		if exp := q.Get("expiresAt"); exp != "" {
			ts, err := time.Parse(time.RFC3339, exp)
			if err != nil {
				http.Error(w, "Malformed expiry.", http.StatusBadRequest)
				trySend(resCh, result{err: fmt.Errorf("reading the expiry: %w", err)})
				return
			}
			s.ExpiresAt = ts
		}
		fmt.Fprint(w, "Signed in. You can close this window and return to the terminal.")
		trySend(resCh, result{s: s})
	})
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	defer srv.Close()

	u, err := url.Parse(base + CLILoginPath)
	if err != nil {
		return Session{}, err
	}
	q := url.Values{}
	q.Set("port", strconv.Itoa(port))
	q.Set("state", state)
	q.Set("publicKey", base64.RawURLEncoding.EncodeToString(spki))
	if opts.Username != "" {
		q.Set("username", opts.Username)
	}
	u.RawQuery = q.Encode()

	fmt.Fprintf(out, "Sign in in your browser:\n  %s\n", u)
	if open != nil {
		if err := open(u.String()); err != nil {
			fmt.Fprintln(out, "(couldn't open a browser; open the link yourself on this machine, or sign in with --username and --password-stdin)")
		}
	}

	ctx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()
	select {
	case r := <-resCh:
		return r.s, r.err
	case <-ctx.Done():
		return Session{}, errors.New("timed out waiting for the browser sign-in; without a browser on this machine, use --username and --password-stdin")
	}
}

// OpenBrowser opens url with the platform's opener, passing it as a single
// argument so no shell sees it.
func OpenBrowser(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return err
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("refusing to open a %s URL", u.Scheme)
	}
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", rawURL).Start()
	case "linux", "freebsd":
		return exec.Command("xdg-open", rawURL).Start()
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", rawURL).Start()
	default:
		return fmt.Errorf("no browser opener for %s", runtime.GOOS)
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
