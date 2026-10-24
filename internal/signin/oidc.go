package signin

// Single sign-in (HYVE-PROPOSAL.md, "Single sign-in"): the host is an
// OpenID Connect client. The browser goes to the identity provider from
// /v1-public/oidc/start and comes back to /v1-public/oidc/callback, which
// makes the user a session exactly as a password sign-in does. Users from
// the provider become User objects on first sign-in (spec.provider and
// spec.subject, no password); their groups come from the ID token at each
// sign-in and are kept on the session, so ClusterAccess and RoleBindings
// can name the provider's groups (as hyve:<group>).

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	hyvev1alpha1 "github.com/cbridges1/hyve/internal/apis/hyve/v1alpha1"
)

// OIDCConfig configures single sign-in (the chart's auth.oidc).
type OIDCConfig struct {
	Issuer       string
	ClientID     string
	ClientSecret string
	// PublicURL is the host's address as browsers reach it; the callback
	// is PublicURL + /v1-public/oidc/callback.
	PublicURL string
	// DisplayName is the provider's name on the sign-in page.
	DisplayName string
	// Scopes besides openid; default profile and email.
	Scopes []string
	// GroupsClaim names the ID token claim holding the user's groups
	// (default "groups"); UsernameClaim the one a new User's name comes
	// from (default "preferred_username", then the email's local part).
	GroupsClaim   string
	UsernameClaim string
	// AllowedDomains, if set, admits only verified emails in these domains;
	// AllowedGroups only users in at least one of these groups.
	AllowedDomains []string
	AllowedGroups  []string
}

// ProviderOIDC is spec.provider for users from the identity provider.
const ProviderOIDC = "oidc"

const (
	oidcStateCookie = "hyve_oidc"
	oidcStateTTL    = 10 * time.Minute
	oidcKeySecret   = "hyve-oidc-state-key"
	// labelSubject on a User: a hash of the provider's issuer and subject,
	// for finding the User again (labels can't hold every subject).
	labelSubject = "hyve.io/oidc-subject"
)

// OIDC runs the sign-in. The provider's discovery document is fetched on
// first use, so the host starts while the provider is down.
type OIDC struct {
	Config OIDCConfig
	Store  *Store

	mu       sync.Mutex
	provider *oidc.Provider
	key      []byte
}

func (o *OIDC) groupsClaim() string {
	if o.Config.GroupsClaim != "" {
		return o.Config.GroupsClaim
	}
	return "groups"
}

func (o *OIDC) callbackURL() string {
	return strings.TrimRight(o.Config.PublicURL, "/") + "/v1-public/oidc/callback"
}

func (o *OIDC) init(ctx context.Context) (*oidc.Provider, []byte, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.key == nil {
		key, err := o.stateKey(ctx)
		if err != nil {
			return nil, nil, fmt.Errorf("the sign-in state key: %w", err)
		}
		o.key = key
	}
	if o.provider == nil {
		p, err := oidc.NewProvider(ctx, o.Config.Issuer)
		if err != nil {
			return nil, nil, fmt.Errorf("the identity provider at %s: %w", o.Config.Issuer, err)
		}
		o.provider = p
	}
	return o.provider, o.key, nil
}

// stateKey signs the state cookie. It's a Secret, so every replica of the
// host accepts a sign-in another one started.
func (o *OIDC) stateKey(ctx context.Context) ([]byte, error) {
	var sec corev1.Secret
	key := client.ObjectKey{Namespace: o.Store.Namespace, Name: oidcKeySecret}
	err := o.Store.Client.Get(ctx, key, &sec)
	if err == nil && len(sec.Data["key"]) >= 32 {
		return sec.Data["key"], nil
	}
	if err != nil && !apierrors.IsNotFound(err) {
		return nil, err
	}
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	sec = corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Namespace: o.Store.Namespace, Name: oidcKeySecret},
		Type:       corev1.SecretTypeOpaque,
		Data:       map[string][]byte{"key": b},
	}
	if err := o.Store.Client.Create(ctx, &sec); err != nil {
		if apierrors.IsAlreadyExists(err) {
			if err := o.Store.Client.Get(ctx, key, &sec); err != nil {
				return nil, err
			}
			return sec.Data["key"], nil
		}
		return nil, err
	}
	return b, nil
}

func (o *OIDC) oauth(p *oidc.Provider) oauth2.Config {
	scopes := []string{oidc.ScopeOpenID}
	if len(o.Config.Scopes) == 0 {
		scopes = append(scopes, "profile", "email")
	}
	for _, s := range o.Config.Scopes {
		if s != oidc.ScopeOpenID {
			scopes = append(scopes, s)
		}
	}
	return oauth2.Config{
		ClientID:     o.Config.ClientID,
		ClientSecret: o.Config.ClientSecret,
		Endpoint:     p.Endpoint(),
		RedirectURL:  o.callbackURL(),
		Scopes:       scopes,
	}
}

// oidcState travels in a signed, HttpOnly cookie between start and
// callback.
type oidcState struct {
	State    string `json:"s"`
	Nonce    string `json:"n"`
	Verifier string `json:"v"`
	Redirect string `json:"r"`
	Expires  int64  `json:"e"`
}

func (o *OIDC) seal(key []byte, st oidcState) (string, error) {
	b, err := json.Marshal(st)
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, key)
	mac.Write(b)
	return base64.RawURLEncoding.EncodeToString(b) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

func (o *OIDC) open(key []byte, v string) (oidcState, error) {
	var st oidcState
	payload, sig, ok := strings.Cut(v, ".")
	if !ok {
		return st, errors.New("malformed sign-in state")
	}
	b, err1 := base64.RawURLEncoding.DecodeString(payload)
	s, err2 := base64.RawURLEncoding.DecodeString(sig)
	if err1 != nil || err2 != nil {
		return st, errors.New("malformed sign-in state")
	}
	mac := hmac.New(sha256.New, key)
	mac.Write(b)
	if !hmac.Equal(mac.Sum(nil), s) {
		return st, errors.New("the sign-in state doesn't verify")
	}
	if err := json.Unmarshal(b, &st); err != nil {
		return st, err
	}
	if time.Now().Unix() > st.Expires {
		return st, errors.New("the sign-in took too long; try again")
	}
	return st, nil
}

func randomString() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// safeRedirect keeps redirects on this host: a path, not a URL.
func safeRedirect(r string) string {
	if !strings.HasPrefix(r, "/") || strings.HasPrefix(r, "//") || strings.HasPrefix(r, "/\\") {
		return "/"
	}
	return r
}

func (s *Server) handleOIDCStart(w http.ResponseWriter, r *http.Request) {
	o := s.OIDC
	p, key, err := o.init(r.Context())
	if err != nil {
		log.Printf("signin: oidc: %v", err)
		http.Redirect(w, r, "/login?error="+url.QueryEscape("The identity provider isn't reachable; try again, or sign in with a password."), http.StatusFound)
		return
	}
	st := oidcState{Redirect: safeRedirect(r.URL.Query().Get("redirect")), Expires: time.Now().Add(oidcStateTTL).Unix(), Verifier: oauth2.GenerateVerifier()}
	if st.State, err = randomString(); err == nil {
		st.Nonce, err = randomString()
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "ServerError", "couldn't start the sign-in")
		return
	}
	v, err := o.seal(key, st)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "ServerError", "couldn't start the sign-in")
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: oidcStateCookie, Value: v, Path: "/v1-public/oidc/", MaxAge: int(oidcStateTTL / time.Second),
		HttpOnly: true, Secure: secure(r), SameSite: http.SameSiteLaxMode,
	})
	cfg := o.oauth(p)
	http.Redirect(w, r, cfg.AuthCodeURL(st.State, oidc.Nonce(st.Nonce), oauth2.S256ChallengeOption(st.Verifier)), http.StatusFound)
}

// loginError sends the browser back to the sign-in page with a message.
func loginError(w http.ResponseWriter, r *http.Request, redirect, msg string) {
	q := url.Values{"error": {msg}}
	if redirect != "" && redirect != "/" {
		q.Set("redirect", redirect)
	}
	http.Redirect(w, r, "/login?"+q.Encode(), http.StatusFound)
}

func (s *Server) handleOIDCCallback(w http.ResponseWriter, r *http.Request) {
	o := s.OIDC
	ctx := r.Context()
	p, key, err := o.init(ctx)
	if err != nil {
		log.Printf("signin: oidc: %v", err)
		loginError(w, r, "", "The identity provider isn't reachable; try again.")
		return
	}
	c, err := r.Cookie(oidcStateCookie)
	if err != nil {
		loginError(w, r, "", "The sign-in expired or was started elsewhere; try again.")
		return
	}
	http.SetCookie(w, &http.Cookie{Name: oidcStateCookie, Value: "", Path: "/v1-public/oidc/", MaxAge: -1, HttpOnly: true, Secure: secure(r), SameSite: http.SameSiteLaxMode})
	st, err := o.open(key, c.Value)
	if err != nil {
		loginError(w, r, "", err.Error())
		return
	}
	q := r.URL.Query()
	if e := q.Get("error"); e != "" {
		msg := q.Get("error_description")
		if msg == "" {
			msg = e
		}
		loginError(w, r, st.Redirect, "The identity provider refused: "+msg)
		return
	}
	if !hmac.Equal([]byte(q.Get("state")), []byte(st.State)) {
		loginError(w, r, st.Redirect, "The sign-in state doesn't match; try again.")
		return
	}
	cfg := o.oauth(p)
	tok, err := cfg.Exchange(ctx, q.Get("code"), oauth2.VerifierOption(st.Verifier))
	if err != nil {
		log.Printf("signin: oidc: exchanging the code: %v", err)
		loginError(w, r, st.Redirect, "The identity provider didn't accept the sign-in.")
		return
	}
	raw, _ := tok.Extra("id_token").(string)
	if raw == "" {
		loginError(w, r, st.Redirect, "The identity provider returned no ID token.")
		return
	}
	idt, err := p.Verifier(&oidc.Config{ClientID: o.Config.ClientID}).Verify(ctx, raw)
	if err != nil {
		log.Printf("signin: oidc: verifying the ID token: %v", err)
		loginError(w, r, st.Redirect, "The ID token didn't verify.")
		return
	}
	if !hmac.Equal([]byte(idt.Nonce), []byte(st.Nonce)) {
		loginError(w, r, st.Redirect, "The ID token's nonce doesn't match; try again.")
		return
	}
	var claims map[string]any
	if err := idt.Claims(&claims); err != nil {
		loginError(w, r, st.Redirect, "The ID token's claims can't be read.")
		return
	}
	who, err := o.identity(idt.Subject, claims)
	if err != nil {
		loginError(w, r, st.Redirect, err.Error())
		return
	}
	u, err := o.user(ctx, who)
	if errors.Is(err, ErrInvalidCredentials) {
		loginError(w, r, st.Redirect, "This account is disabled.")
		return
	}
	if err != nil {
		log.Printf("signin: oidc: the User for %s: %v", who.subject, err)
		loginError(w, r, st.Redirect, "Couldn't sign you in; see the host's log.")
		return
	}
	token, expiresAt, err := o.Store.createSessionWithGroups(ctx, u, who.groups)
	if err != nil {
		log.Printf("signin: oidc: session for %s: %v", u.Name, err)
		loginError(w, r, st.Redirect, "Couldn't start a session.")
		return
	}
	o.Store.recordLogin(ctx, u)
	setSessionCookie(w, r, token, expiresAt)
	http.Redirect(w, r, st.Redirect, http.StatusFound)
}

type oidcIdentity struct {
	subject, username, displayName, email string
	groups                                []string
}

func claimString(claims map[string]any, k string) string {
	s, _ := claims[k].(string)
	return s
}

func claimStrings(claims map[string]any, k string) []string {
	switch v := claims[k].(type) {
	case string:
		return []string{v}
	case []any:
		out := make([]string, 0, len(v))
		for _, x := range v {
			if s, ok := x.(string); ok && s != "" {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// identity reads who the token says the user is, and applies the allowed
// domains and groups.
func (o *OIDC) identity(subject string, claims map[string]any) (oidcIdentity, error) {
	who := oidcIdentity{
		subject:     subject,
		email:       strings.ToLower(claimString(claims, "email")),
		displayName: claimString(claims, "name"),
		groups:      claimStrings(claims, o.groupsClaim()),
	}
	uc := o.Config.UsernameClaim
	if uc == "" {
		uc = "preferred_username"
	}
	who.username = claimString(claims, uc)
	if who.username == "" && who.email != "" {
		who.username, _, _ = strings.Cut(who.email, "@")
	}
	if len(o.Config.AllowedDomains) > 0 {
		verified, present := claims["email_verified"].(bool)
		_, domain, _ := strings.Cut(who.email, "@")
		ok := domain != "" && (!present || verified)
		if ok {
			ok = false
			for _, d := range o.Config.AllowedDomains {
				if strings.EqualFold(strings.TrimPrefix(d, "@"), domain) {
					ok = true
				}
			}
		}
		if !ok {
			return who, errors.New("Your account's email domain isn't allowed to sign in to this hyve.")
		}
	}
	if len(o.Config.AllowedGroups) > 0 {
		ok := false
		for _, g := range who.groups {
			for _, a := range o.Config.AllowedGroups {
				if g == a {
					ok = true
				}
			}
		}
		if !ok {
			return who, errors.New("You aren't in a group allowed to sign in to this hyve.")
		}
	}
	return who, nil
}

func (o *OIDC) subjectLabel(subject string) string {
	h := sha256.Sum256([]byte(o.Config.Issuer + "|" + subject))
	return hex.EncodeToString(h[:])[:40]
}

var notDNSLabel = regexp.MustCompile(`[^a-z0-9-]+`)

// usernameFrom makes a User name (a DNS label) from what the provider
// calls the user.
func usernameFrom(s string) string {
	s = notDNSLabel.ReplaceAllString(strings.ToLower(s), "-")
	s = strings.Trim(s, "-")
	if len(s) > 56 {
		s = strings.TrimRight(s[:56], "-")
	}
	if s == "" {
		s = "user"
	}
	return s
}

// user finds the User for an identity, or creates it. A name already taken
// (by a password user, or another subject) gets a number: two people never
// share a User.
func (o *OIDC) user(ctx context.Context, who oidcIdentity) (*hyvev1alpha1.User, error) {
	label := o.subjectLabel(who.subject)
	var list hyvev1alpha1.UserList
	if err := o.Store.Client.List(ctx, &list, client.MatchingLabels{labelSubject: label}); err != nil {
		return nil, err
	}
	for i := range list.Items {
		u := &list.Items[i]
		if u.Spec.Provider == ProviderOIDC && u.Spec.Subject == who.subject {
			if u.Spec.Disabled {
				return nil, ErrInvalidCredentials
			}
			if u.Spec.Email != who.email || (who.displayName != "" && u.Spec.DisplayName == "") {
				patch := client.MergeFrom(u.DeepCopy())
				u.Spec.Email = who.email
				if u.Spec.DisplayName == "" {
					u.Spec.DisplayName = who.displayName
				}
				if err := o.Store.Client.Patch(ctx, u, patch); err != nil {
					log.Printf("signin: oidc: updating %s: %v", u.Name, err)
				}
			}
			return u, nil
		}
	}
	base := usernameFrom(who.username)
	for n := 1; n < 100; n++ {
		name := base
		if n > 1 {
			name = fmt.Sprintf("%s-%d", base, n)
		}
		u := &hyvev1alpha1.User{
			ObjectMeta: metav1.ObjectMeta{Name: name, Labels: map[string]string{labelSubject: label}},
			Spec: hyvev1alpha1.UserSpec{
				DisplayName: who.displayName,
				Provider:    ProviderOIDC,
				Subject:     who.subject,
				Email:       who.email,
			},
		}
		err := o.Store.Client.Create(ctx, u)
		if apierrors.IsAlreadyExists(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		log.Printf("signin: oidc: created User %s for %s", name, who.email)
		return u, nil
	}
	return nil, fmt.Errorf("no free username for %q", base)
}

// AuthMethods is GET /v1-public/auth-methods: how the sign-in page offers
// to sign in.
type AuthMethods struct {
	Passwords bool        `json:"passwords"`
	OIDC      *OIDCMethod `json:"oidc,omitempty"`
}

// OIDCMethod is the identity provider, as the sign-in page shows it.
type OIDCMethod struct {
	DisplayName string `json:"displayName"`
	StartURL    string `json:"startUrl"`
}

func (s *Server) handleAuthMethods(w http.ResponseWriter, _ *http.Request) {
	m := AuthMethods{Passwords: !s.NoPasswords}
	if s.OIDC != nil {
		name := s.OIDC.Config.DisplayName
		if name == "" {
			name = "single sign-on"
		}
		m.OIDC = &OIDCMethod{DisplayName: name, StartURL: "/v1-public/oidc/start"}
	}
	writeJSON(w, http.StatusOK, m)
}
