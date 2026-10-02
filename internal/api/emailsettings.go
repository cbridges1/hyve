package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	hyvev1alpha1 "github.com/cbridges1/hyve/internal/apis/hyve/v1alpha1"
	"github.com/cbridges1/hyve/internal/email"
	"github.com/cbridges1/hyve/internal/orgdb"
)

// SeedEmailSettings seeds email_settings from operator-supplied bootstrap
// values (cmd/api/run.go's --smtp-* flags, typically fed by Helm
// values/a mounted secret) — but only if no SMTP host has ever been
// saved (EmailSettings.Configured() == false). Safe to call on every
// startup, the same "seed once, the store's own state owns it after
// that" idiom EnsureSigningKey already established: once a superadmin
// has saved anything through PATCH /api/system/email, this never
// clobbers it with a stale flag value on a later restart. host == "" is
// a no-op — nothing to seed, e.g. an install that only ever configures
// SMTP through the console.
func SeedEmailSettings(ctx context.Context, store *orgdb.Store, host string, port int, username, password, fromAddress string) error {
	if host == "" {
		return nil
	}
	existing, err := store.GetEmailSettings(ctx)
	if err != nil {
		return fmt.Errorf("check existing email settings: %w", err)
	}
	if existing.Configured() {
		return nil
	}

	settings := orgdb.EmailSettings{SMTPHost: host, SMTPPort: port, FromAddress: fromAddress}
	if username != "" {
		settings.SMTPUsername = &username
	}
	if password != "" {
		settings.SMTPPassword = &password
	}
	if _, err := store.UpsertEmailSettings(ctx, settings); err != nil {
		return fmt.Errorf("seed email settings: %w", err)
	}
	return nil
}

// registerEmailSettingsRoutes wires the install-wide SMTP settings
// surface — superadmin-only, same tier as POST /organizations, not
// per-tenant (see emailSettingsDTO's own doc comment for why this isn't
// scoped by "act as" the way most other /api/* endpoints are).
func (s *Server) registerEmailSettingsRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /system/email", s.handleGetEmailSettings)
	mux.HandleFunc("PATCH /system/email", s.handleUpdateEmailSettings)
	mux.HandleFunc("POST /system/email/test", s.handleTestEmail)
}

// emailSettingsDTO never echoes the SMTP password back — PasswordSet
// (a boolean) is all a caller ever learns about it, same "never echo a
// secret" stance accountDTO already takes with password hashes.
// Install-wide, not per-organization: email delivery is a control-plane
// concern (which physical SMTP relay this hyve-api process talks to),
// unlike HyveConfig which genuinely varies per reconciling cluster — see
// HYVE-EMAIL-IMPLEMENTATION-PLAN.md's "Where settings live" section for
// why this intentionally isn't organization-scoped at all.
type emailSettingsDTO struct {
	SMTPHost     string  `json:"smtpHost"`
	SMTPPort     int     `json:"smtpPort"`
	SMTPUsername *string `json:"smtpUsername,omitempty"`
	PasswordSet  bool    `json:"passwordSet"`
	UseTLS       bool    `json:"useTls"`
	SkipVerify   bool    `json:"skipVerify"`
	FromAddress  string  `json:"fromAddress"`
	FromName     *string `json:"fromName,omitempty"`
	Configured   bool    `json:"configured"`
}

func toEmailSettingsDTO(e orgdb.EmailSettings) emailSettingsDTO {
	return emailSettingsDTO{
		SMTPHost:     e.SMTPHost,
		SMTPPort:     e.SMTPPort,
		SMTPUsername: e.SMTPUsername,
		PasswordSet:  e.SMTPPassword != nil && *e.SMTPPassword != "",
		UseTLS:       e.UseTLS,
		SkipVerify:   e.SkipVerify,
		FromAddress:  e.FromAddress,
		FromName:     e.FromName,
		Configured:   e.Configured(),
	}
}

func (s *Server) handleGetEmailSettings(w http.ResponseWriter, r *http.Request) {
	if !RequireRole(w, r, hyvev1alpha1.RoleSuperadmin) {
		return
	}
	settings, err := s.OrgStore.GetEmailSettings(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load email settings")
		return
	}
	writeJSON(w, http.StatusOK, toEmailSettingsDTO(settings))
}

// updateEmailSettingsRequest fields are all nil-means-"leave unchanged" —
// a caller PATCHing just, say, SkipVerify shouldn't have to resend the
// password too. SMTPPassword's nil-vs-empty-string distinction is the one
// that matters most: omitted leaves the stored password untouched,
// present-and-empty clears it (going back to an anonymous relay) — same
// three-state shape updateAccountRequest.Email already established.
type updateEmailSettingsRequest struct {
	SMTPHost     *string `json:"smtpHost,omitempty"`
	SMTPPort     *int    `json:"smtpPort,omitempty"`
	SMTPUsername *string `json:"smtpUsername,omitempty"`
	SMTPPassword *string `json:"smtpPassword,omitempty"`
	UseTLS       *bool   `json:"useTls,omitempty"`
	SkipVerify   *bool   `json:"skipVerify,omitempty"`
	FromAddress  *string `json:"fromAddress,omitempty"`
	FromName     *string `json:"fromName,omitempty"`
}

func (s *Server) handleUpdateEmailSettings(w http.ResponseWriter, r *http.Request) {
	if !RequireRole(w, r, hyvev1alpha1.RoleSuperadmin) {
		return
	}
	var req updateEmailSettingsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	ctx := r.Context()
	current, err := s.OrgStore.GetEmailSettings(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load email settings")
		return
	}

	if req.SMTPHost != nil {
		current.SMTPHost = *req.SMTPHost
	}
	if req.SMTPPort != nil {
		current.SMTPPort = *req.SMTPPort
	}
	if req.SMTPUsername != nil {
		current.SMTPUsername = nilIfEmpty(*req.SMTPUsername)
	}
	if req.SMTPPassword != nil {
		current.SMTPPassword = nilIfEmpty(*req.SMTPPassword)
	}
	if req.UseTLS != nil {
		current.UseTLS = *req.UseTLS
	}
	if req.SkipVerify != nil {
		current.SkipVerify = *req.SkipVerify
	}
	if req.FromAddress != nil {
		current.FromAddress = *req.FromAddress
	}
	if req.FromName != nil {
		current.FromName = nilIfEmpty(*req.FromName)
	}

	updated, err := s.OrgStore.UpsertEmailSettings(ctx, current)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save email settings")
		return
	}
	writeJSON(w, http.StatusOK, toEmailSettingsDTO(updated))
}

// nilIfEmpty turns "" into nil — the clear-this-field half of the
// nil-vs-empty-string convention used throughout this file.
func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

type testEmailRequest struct {
	To string `json:"to"`
}

// handleTestEmail is the one place a broken SMTP config should be loud,
// not soft — everywhere else in this API, a failed send is logged and
// swallowed (the underlying account action already succeeded and that's
// what the caller asked for), but an admin actively configuring SMTP
// needs to know immediately whether it actually works, not discover it
// days later when the first real password reset silently goes nowhere.
func (s *Server) handleTestEmail(w http.ResponseWriter, r *http.Request) {
	if !RequireRole(w, r, hyvev1alpha1.RoleSuperadmin) {
		return
	}
	var req testEmailRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.To == "" {
		writeError(w, http.StatusBadRequest, "to is required")
		return
	}

	requestedBy, _ := UsernameFromContext(r.Context())
	err := email.Send(r.Context(), s.OrgStore, req.To, email.TemplateTest, email.TestEmailData{RequestedBy: requestedBy})
	if errors.Is(err, email.ErrNotConfigured) {
		writeError(w, http.StatusBadRequest, "email is not configured — save SMTP settings first")
		return
	}
	if err != nil {
		writeError(w, http.StatusBadGateway, "failed to send test email: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"sent": true})
}
