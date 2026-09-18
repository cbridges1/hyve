package email

import (
	"context"
	"fmt"

	mail "github.com/wneessen/go-mail"

	"github.com/cbridges1/hyve/internal/orgdb"
)

// defaultFromName is used when EmailSettings.FromName is unset — the
// install-wide equivalent of Pangolin's own BRANDING_APP_NAME fallback.
const defaultFromName = "hyve"

// Send renders tmpl with data and delivers it to "to" using the current
// install-wide SMTP settings, read fresh from store on every call — a
// just-saved settings change (PATCH /api/system/email) takes effect on
// the very next send, no process restart needed, the same "runtime-
// editable, no caching to invalidate" property every other setting this
// API exposes already has.
//
// Returns ErrNotConfigured if no SMTP host has ever been saved. That is
// not a failure this function itself reacts to — every call site decides
// what "no SMTP configured" should mean for its own request (see
// internal/api's own callers: the forgot-password handler falls back to
// logging the token, the test-email endpoint surfaces it as a normal
// error).
func Send(ctx context.Context, store *orgdb.Store, to string, tmpl Template, data any) error {
	settings, err := store.GetEmailSettings(ctx)
	if err != nil {
		return fmt.Errorf("load email settings: %w", err)
	}
	if !settings.Configured() {
		return ErrNotConfigured
	}

	subject, body, err := render(tmpl, data)
	if err != nil {
		return fmt.Errorf("render email: %w", err)
	}

	client, err := newClient(settings)
	if err != nil {
		return fmt.Errorf("build smtp client: %w", err)
	}

	fromName := defaultFromName
	if settings.FromName != nil && *settings.FromName != "" {
		fromName = *settings.FromName
	}

	msg := mail.NewMsg()
	if err := msg.FromFormat(fromName, settings.FromAddress); err != nil {
		return fmt.Errorf("set from address: %w", err)
	}
	if err := msg.To(to); err != nil {
		return fmt.Errorf("set recipient: %w", err)
	}
	msg.Subject(subject)
	msg.SetBodyString(mail.TypeTextHTML, body)

	if err := client.DialAndSendWithContext(ctx, msg); err != nil {
		return fmt.Errorf("send email: %w", err)
	}
	return nil
}
