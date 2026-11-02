package signin

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"
	"time"

	authorizationv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/cbridges1/hyve/internal/email"
)

// EmailSecret holds the host's SMTP settings, in the host's namespace: one
// install-wide relay, password included, so it's a Secret. Who may read or
// change it is who may get or update that Secret.
const EmailSecret = "hyve-email"

const (
	keySMTPHost     = "host"
	keySMTPPort     = "port"
	keySMTPUsername = "username"
	keySMTPPassword = "password"
	keySMTPTLS      = "useTLS"
	keySMTPSkip     = "skipVerify"
	keyFromAddress  = "fromAddress"
	keyFromName     = "fromName"

	// mailTimeout bounds one send that runs after its request returned.
	mailTimeout = time.Minute
)

// EmailSettings returns the SMTP settings; empty when there are none.
func (s *Store) EmailSettings(ctx context.Context) (email.Settings, error) {
	var sec corev1.Secret
	err := s.Client.Get(ctx, client.ObjectKey{Namespace: s.Namespace, Name: EmailSecret}, &sec)
	if apierrors.IsNotFound(err) {
		return email.Settings{}, nil
	}
	if err != nil {
		return email.Settings{}, err
	}
	d := func(k string) string { return string(sec.Data[k]) }
	port, _ := strconv.Atoi(d(keySMTPPort))
	return email.Settings{
		Host:        d(keySMTPHost),
		Port:        port,
		Username:    d(keySMTPUsername),
		Password:    d(keySMTPPassword),
		UseTLS:      d(keySMTPTLS) == "true",
		SkipVerify:  d(keySMTPSkip) == "true",
		FromAddress: d(keyFromAddress),
		FromName:    d(keyFromName),
	}, nil
}

// SaveEmailSettings writes the SMTP settings.
func (s *Store) SaveEmailSettings(ctx context.Context, e email.Settings) error {
	data := map[string][]byte{
		keySMTPHost:     []byte(e.Host),
		keySMTPPort:     []byte(strconv.Itoa(e.Port)),
		keySMTPUsername: []byte(e.Username),
		keySMTPPassword: []byte(e.Password),
		keySMTPTLS:      []byte(strconv.FormatBool(e.UseTLS)),
		keySMTPSkip:     []byte(strconv.FormatBool(e.SkipVerify)),
		keyFromAddress:  []byte(e.FromAddress),
		keyFromName:     []byte(e.FromName),
	}
	sec := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: s.Namespace, Name: EmailSecret}}
	err := s.Client.Get(ctx, client.ObjectKeyFromObject(sec), sec)
	if apierrors.IsNotFound(err) {
		sec.Type = corev1.SecretTypeOpaque
		sec.Data = data
		return s.Client.Create(ctx, sec)
	}
	if err != nil {
		return err
	}
	sec.Data = data
	return s.Client.Update(ctx, sec)
}

// SeedEmailSettings saves e (the chart's email.smtp values) unless SMTP is
// already configured: after the first start, the settings page owns them.
func (s *Store) SeedEmailSettings(ctx context.Context, e email.Settings) error {
	if !e.Configured() {
		return nil
	}
	cur, err := s.EmailSettings(ctx)
	if err != nil {
		return err
	}
	if cur.Configured() {
		return nil
	}
	return s.SaveEmailSettings(ctx, e)
}

// sendMail sends one email with the current settings.
func (s *Server) sendMail(ctx context.Context, to string, tmpl email.Template, data any) error {
	settings, err := s.Store.EmailSettings(ctx)
	if err != nil {
		return err
	}
	if s.Mailer != nil {
		if !settings.Configured() {
			return email.ErrNotConfigured
		}
		return s.Mailer(ctx, settings, to, tmpl, data)
	}
	return email.Send(ctx, settings, to, tmpl, data)
}

// notify sends a notice in the background: the change it reports has
// happened either way, so a failure is only logged, and no SMTP at all
// isn't worth a line.
func (s *Server) notify(to string, tmpl email.Template, data any) {
	if to == "" {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), mailTimeout)
		defer cancel()
		if err := s.sendMail(ctx, to, tmpl, data); err != nil && !errors.Is(err, email.ErrNotConfigured) {
			log.Printf("signin: sending %s to %s: %v", tmpl, to, err)
		}
	}()
}

// userEmail is the address on username's User, or "".
func (s *Server) userEmail(ctx context.Context, username string) string {
	u, err := s.Store.getUser(ctx, username)
	if err != nil {
		return ""
	}
	return u.Spec.Email
}

// EmailSettingsView is GET /v1/settings/email. The SMTP password is never
// sent back: PasswordSet says whether there is one.
type EmailSettingsView struct {
	Host        string `json:"host"`
	Port        int    `json:"port"`
	Username    string `json:"username"`
	PasswordSet bool   `json:"passwordSet"`
	UseTLS      bool   `json:"useTLS"`
	SkipVerify  bool   `json:"skipVerify"`
	FromAddress string `json:"fromAddress"`
	FromName    string `json:"fromName"`
	Configured  bool   `json:"configured"`
}

func viewOf(e email.Settings) EmailSettingsView {
	port := e.Port
	if port == 0 {
		port = 587
	}
	return EmailSettingsView{
		Host: e.Host, Port: port, Username: e.Username, PasswordSet: e.Password != "",
		UseTLS: e.UseTLS, SkipVerify: e.SkipVerify, FromAddress: e.FromAddress, FromName: e.FromName,
		Configured: e.Configured(),
	}
}

// EmailSettingsUpdate is PUT /v1/settings/email. A field left out keeps its
// value; Password "" removes the password (an anonymous relay).
type EmailSettingsUpdate struct {
	Host        *string `json:"host,omitempty"`
	Port        *int    `json:"port,omitempty"`
	Username    *string `json:"username,omitempty"`
	Password    *string `json:"password,omitempty"`
	UseTLS      *bool   `json:"useTLS,omitempty"`
	SkipVerify  *bool   `json:"skipVerify,omitempty"`
	FromAddress *string `json:"fromAddress,omitempty"`
	FromName    *string `json:"fromName,omitempty"`
}

// mayEmailSettings checks, as the caller, verb on the EmailSecret Secret,
// writing the error response when the answer is no.
func (s *Server) mayEmailSettings(w http.ResponseWriter, r *http.Request, verb string) (Identity, bool) {
	ident, ok := s.authenticate(w, r)
	if !ok {
		return Identity{}, false
	}
	sar := &authorizationv1.SubjectAccessReview{Spec: authorizationv1.SubjectAccessReviewSpec{
		User:   ident.KubeUser(),
		Groups: ident.KubeGroups(),
		ResourceAttributes: &authorizationv1.ResourceAttributes{
			Namespace: s.Store.Namespace, Resource: "secrets", Name: EmailSecret, Verb: verb, Version: "v1",
		},
	}}
	if err := s.Store.Client.Create(r.Context(), sar); err != nil {
		log.Printf("signin: access review for %s on the email settings: %v", ident.Username, err)
		writeError(w, http.StatusInternalServerError, "ServerError", "couldn't check permissions")
		return Identity{}, false
	}
	if !sar.Status.Allowed {
		writeError(w, http.StatusForbidden, "Forbidden", "you can't "+verb+" the email settings")
		return Identity{}, false
	}
	return ident, true
}

func (s *Server) handleGetEmailSettings(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.mayEmailSettings(w, r, "get"); !ok {
		return
	}
	e, err := s.Store.EmailSettings(r.Context())
	if err != nil {
		log.Printf("signin: reading the email settings: %v", err)
		writeError(w, http.StatusInternalServerError, "ServerError", "couldn't read the email settings")
		return
	}
	writeJSON(w, http.StatusOK, viewOf(e))
}

func (s *Server) handleUpdateEmailSettings(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.mayEmailSettings(w, r, "update"); !ok {
		return
	}
	var req EmailSettingsUpdate
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "InvalidBodyContent", "the request body isn't valid JSON")
		return
	}
	e, err := s.Store.EmailSettings(r.Context())
	if err != nil {
		log.Printf("signin: reading the email settings: %v", err)
		writeError(w, http.StatusInternalServerError, "ServerError", "couldn't read the email settings")
		return
	}
	set := func(dst *string, v *string) {
		if v != nil {
			*dst = *v
		}
	}
	set(&e.Host, req.Host)
	set(&e.Username, req.Username)
	set(&e.Password, req.Password)
	set(&e.FromAddress, req.FromAddress)
	set(&e.FromName, req.FromName)
	if req.Port != nil {
		e.Port = *req.Port
	}
	if req.UseTLS != nil {
		e.UseTLS = *req.UseTLS
	}
	if req.SkipVerify != nil {
		e.SkipVerify = *req.SkipVerify
	}
	if e.Port < 0 || e.Port > 65535 {
		writeError(w, http.StatusUnprocessableEntity, "InvalidFormat", "the port must be between 1 and 65535")
		return
	}
	if e.Host != "" && e.FromAddress == "" {
		writeError(w, http.StatusUnprocessableEntity, "InvalidFormat", "a from address is needed to send mail")
		return
	}
	if err := s.Store.SaveEmailSettings(r.Context(), e); err != nil {
		log.Printf("signin: saving the email settings: %v", err)
		writeError(w, http.StatusInternalServerError, "ServerError", "couldn't save the email settings")
		return
	}
	writeJSON(w, http.StatusOK, viewOf(e))
}

// handleTestEmail sends a test email and, unlike every other send, reports
// the failure: whoever is setting SMTP up needs to know now, not when the
// first password reset goes nowhere.
func (s *Server) handleTestEmail(w http.ResponseWriter, r *http.Request) {
	ident, ok := s.mayEmailSettings(w, r, "update")
	if !ok {
		return
	}
	var req struct {
		To string `json:"to"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req); err != nil || req.To == "" {
		writeError(w, http.StatusBadRequest, "InvalidBodyContent", `send {"to": "<address>"}`)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), mailTimeout)
	defer cancel()
	err := s.sendMail(ctx, req.To, email.TemplateTest, email.TestEmailData{RequestedBy: ident.Username})
	switch {
	case errors.Is(err, email.ErrNotConfigured):
		writeError(w, http.StatusBadRequest, "NotConfigured", "email isn't configured: save the SMTP settings first")
	case err != nil:
		writeError(w, http.StatusBadGateway, "SendFailed", "the test email failed: "+err.Error())
	default:
		writeJSON(w, http.StatusOK, map[string]bool{"sent": true})
	}
}
