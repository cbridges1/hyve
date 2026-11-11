package signin

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	apierrors "k8s.io/apimachinery/pkg/api/errors"

	hyvev1alpha1 "github.com/cbridges1/hyve/internal/apis/hyve/v1alpha1"
)

// A CI job signs in with the ID token its CI system gives it (GitHub
// Actions, GitLab CI, Buildkite...), as a User whose spec.trustedCI
// trusts that system's jobs with those claims. The pipeline keeps no
// secret: the token is the job's own, lasts minutes, and is good only for
// its audience. The session lasts an hour and can't make API tokens.

const (
	// FederatedSessionTTL is how long a CI job's session lasts.
	FederatedSessionTTL = time.Hour
	// DefaultCIAudience is the audience a job's token must have when the
	// TrustedCI entry names none.
	DefaultCIAudience = "hyve"
)

// bindingClaims say which repository or project a token is from. A
// TrustedCI entry must pin one of them, or any job on the CI system (any
// repository on github.com) could sign in.
var bindingClaims = []string{"sub", "repository", "repository_owner", "repository_id", "repository_owner_id",
	"project_path", "project_id", "namespace_path", "namespace_id", "pipeline_slug", "organization_slug"}

// ValidateTrustedCI checks an entry: an https issuer and at least one
// claim that pins a repository or project to something narrower than '*'.
func ValidateTrustedCI(t hyvev1alpha1.TrustedCI) error {
	if !ValidUsername(t.Name) {
		return errors.New("a name must be a DNS label: lowercase letters, digits and '-'")
	}
	if !strings.HasPrefix(t.Issuer, "https://") {
		return errors.New("the issuer must be an https:// URL")
	}
	for _, k := range bindingClaims {
		if v, ok := t.Claims[k]; ok && strings.Trim(v, "*") != "" {
			return nil
		}
	}
	return fmt.Errorf("pin the repository or project with one of these claims: %s", strings.Join(bindingClaims, ", "))
}

// claimMatches compares a claim with a TrustedCI value, where '*' matches
// anything.
func claimMatches(pattern string, v any) bool {
	var s string
	switch x := v.(type) {
	case string:
		s = x
	case float64, bool, json.Number:
		s = fmt.Sprint(x)
	default:
		return false
	}
	parts := strings.Split(pattern, "*")
	for i, p := range parts {
		parts[i] = regexp.QuoteMeta(p)
	}
	return regexp.MustCompile("^" + strings.Join(parts, ".*") + "$").MatchString(s)
}

// trusts returns the entry of u's that a verified token's claims match,
// or nil.
func trusts(entries []hyvev1alpha1.TrustedCI, issuer string, aud []string, claims map[string]any) *hyvev1alpha1.TrustedCI {
	for i := range entries {
		t := &entries[i]
		if t.Issuer != issuer || ValidateTrustedCI(*t) != nil {
			continue
		}
		want := t.Audience
		if want == "" {
			want = DefaultCIAudience
		}
		ok := false
		for _, a := range aud {
			ok = ok || a == want
		}
		for k, p := range t.Claims {
			ok = ok && claimMatches(p, claims[k])
		}
		if ok {
			return t
		}
	}
	return nil
}

// unverifiedIssuer reads a JWT's iss without checking it, to pick the
// issuer to verify it with (only ones a User trusts are ever fetched).
func unverifiedIssuer(raw string) (string, error) {
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		return "", errors.New("not a JWT")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", err
	}
	var c struct {
		Iss string `json:"iss"`
	}
	if err := json.Unmarshal(payload, &c); err != nil {
		return "", err
	}
	return c.Iss, nil
}

// verifiedToken is a checked ID token.
type verifiedToken struct {
	Issuer, Subject string
	Audience        []string
	Claims          map[string]any
}

// ciVerifier checks ID tokens against their issuers' published keys,
// keeping each issuer's provider once discovered.
type ciVerifier struct {
	mu        sync.Mutex
	providers map[string]*oidc.Provider
}

func (v *ciVerifier) verify(ctx context.Context, issuer, raw string) (verifiedToken, error) {
	v.mu.Lock()
	p := v.providers[issuer]
	v.mu.Unlock()
	if p == nil {
		dctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		var err error
		if p, err = oidc.NewProvider(dctx, issuer); err != nil {
			return verifiedToken{}, err
		}
		v.mu.Lock()
		if v.providers == nil {
			v.providers = map[string]*oidc.Provider{}
		}
		v.providers[issuer] = p
		v.mu.Unlock()
	}
	// The audience is checked against the matching entry.
	idt, err := p.Verifier(&oidc.Config{SkipClientIDCheck: true}).Verify(ctx, raw)
	if err != nil {
		return verifiedToken{}, err
	}
	claims := map[string]any{}
	if err := idt.Claims(&claims); err != nil {
		return verifiedToken{}, err
	}
	return verifiedToken{Issuer: idt.Issuer, Subject: idt.Subject, Audience: idt.Audience, Claims: claims}, nil
}

// CILoginRequest is the body of POST /v1-public/ci-login.
type CILoginRequest struct {
	Username string `json:"username"`
	// Token is the CI job's ID token.
	Token string `json:"token"`
}

var errNotTrusted = errors.New("this token isn't trusted to sign in as that user")

// FederatedLogin trades a CI job's ID token for a session as username.
func (s *Store) FederatedLogin(ctx context.Context, verify func(context.Context, string, string) (verifiedToken, error), username, raw string) (string, time.Time, string, error) {
	u, err := s.getUser(ctx, username)
	if apierrors.IsNotFound(err) {
		return "", time.Time{}, "", errNotTrusted
	}
	if err != nil {
		return "", time.Time{}, "", err
	}
	if u.Spec.Disabled || len(u.Spec.TrustedCI) == 0 {
		return "", time.Time{}, "", errNotTrusted
	}
	iss, err := unverifiedIssuer(raw)
	if err != nil {
		return "", time.Time{}, "", errNotTrusted
	}
	known := false
	for _, t := range u.Spec.TrustedCI {
		known = known || t.Issuer == iss
	}
	if !known {
		return "", time.Time{}, "", errNotTrusted
	}
	tok, err := verify(ctx, iss, raw)
	if err != nil {
		return "", time.Time{}, "", fmt.Errorf("%w: %v", errNotTrusted, err)
	}
	t := trusts(u.Spec.TrustedCI, tok.Issuer, tok.Audience, tok.Claims)
	if t == nil {
		return "", time.Time{}, "", fmt.Errorf("%w: no entry matches subject %q", errNotTrusted, tok.Subject)
	}
	via := t.Name + " " + tok.Subject
	if len(via) > 256 {
		via = via[:256]
	}
	token, expiresAt, err := s.createSession(ctx, u, nil, sessionOptions{
		ttl:         FederatedSessionTTL,
		annotations: map[string]string{annotationSessionKind: KindFederated, annotationVia: via},
	})
	if err != nil {
		return "", time.Time{}, "", err
	}
	s.recordLogin(ctx, u)
	return token, expiresAt, via, nil
}

func (s *Server) handleCILogin(w http.ResponseWriter, r *http.Request) {
	var req CILoginRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "InvalidBodyContent", "the request body isn't valid JSON")
		return
	}
	addrKey := "addr:" + clientAddr(r)
	if s.addrLimit.Blocked(addrKey) {
		writeError(w, http.StatusTooManyRequests, "TooManyRequests", "too many failed sign-ins; try again later")
		return
	}
	token, expiresAt, via, err := s.Store.FederatedLogin(r.Context(), s.ci.verify, req.Username, req.Token)
	if errors.Is(err, errNotTrusted) {
		s.addrLimit.Fail(addrKey)
		log.Printf("signin: CI sign-in as %q refused: %v", req.Username, err)
		writeError(w, http.StatusUnauthorized, "Unauthorized", errNotTrusted.Error())
		return
	}
	if err != nil {
		log.Printf("signin: CI sign-in as %q: %v", req.Username, err)
		writeError(w, http.StatusInternalServerError, "ServerError", "sign-in failed")
		return
	}
	s.addrLimit.Succeed(addrKey)
	log.Printf("signin: CI sign-in as %s (%s)", req.Username, via)
	id, _, _ := parseSessionToken(token)
	writeJSON(w, http.StatusCreated, LoginResponse{
		Token: token, ExpiresAt: expiresAt.Format(time.RFC3339), UserID: req.Username,
		ID: SessionPrefix + id, BaseType: "token", Type: "token",
	})
}
