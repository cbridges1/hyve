package email

import (
	"bytes"
	"embed"
	"fmt"
	"html/template"
)

//go:embed templates/*.html
var templatesFS embed.FS

//go:embed templates/layout.html
var layoutSrc string

// Template names one of the emails this package knows how to render — the
// Go analog of Pangolin's server/emails/templates/*.tsx catalog, scoped
// to what actually applies to a self-hosted, no-signup, no-2FA product
// (see HYVE-EMAIL-IMPLEMENTATION-PLAN.md's "Baseline" and "Non-goals"
// sections for what didn't carry over and why).
type Template string

const (
	// TemplatePasswordResetCode is the forgot-password request itself —
	// carries a code and a link, both work (see PasswordResetCodeData).
	TemplatePasswordResetCode Template = "password-reset-code"
	// TemplatePasswordChanged is the after-the-fact security notice sent
	// once a reset (or any other password change) actually completes —
	// mirrors Pangolin's NotifyResetPassword, but fired from every
	// password-change path in this API, not just the reset flow (see
	// PasswordChangedData).
	TemplatePasswordChanged Template = "password-changed"
	// TemplateRoleChanged notifies an account's own holder when their
	// role is promoted or demoted (see RoleChangedData) — no Pangolin
	// analog, new for hyve's role model.
	TemplateRoleChanged Template = "role-changed"
	// TemplateEmailChanged is sent to the OLD address when an account's
	// email is changed (see EmailChangedData) — a classic account-
	// takeover guard: whoever owned the old address still hears about it,
	// at an address that can no longer silently redirect future resets.
	TemplateEmailChanged Template = "email-changed"
	// TemplateAccountCreated notifies a newly created account's holder,
	// if an email was set at creation time (see AccountCreatedData). No
	// password in the body, ever.
	TemplateAccountCreated Template = "account-created"
	// TemplateTest backs POST /api/system/email/test — lets an admin
	// confirm SMTP settings actually work (see TestEmailData).
	TemplateTest Template = "test"
)

// PasswordResetCodeData is TemplatePasswordResetCode's data.
type PasswordResetCodeData struct {
	Username string
	Code     string
	Link     string
}

// PasswordChangedData is TemplatePasswordChanged's data.
type PasswordChangedData struct {
	Username string
}

// RoleChangedData is TemplateRoleChanged's data.
type RoleChangedData struct {
	Username string
	OldRole  string
	NewRole  string
}

// EmailChangedData is TemplateEmailChanged's data — sent to the OLD
// address, so NewEmail is the only address worth naming in the body.
type EmailChangedData struct {
	Username string
	NewEmail string
}

// AccountCreatedData is TemplateAccountCreated's data.
type AccountCreatedData struct {
	Username string
	Role     string
}

// TestEmailData is TemplateTest's data.
type TestEmailData struct {
	RequestedBy string
}

// render renders tmpl's subject and HTML body against data, composing a
// fresh clone of the shared layout with just that one template's own
// "subject"/"body" block definitions parsed into it. Deliberately not one
// combined html/template.ParseFS across every *.html file at once — every
// per-email file defines blocks named "subject" and "body", and Go's
// html/template shares one flat namespace across every file parsed into
// the same *template.Template, so parsing them all together would let
// each file's "body" silently clobber the last one parsed. Cloning the
// base layout per call keeps each template's blocks isolated in their own
// namespace instead.
func render(tmpl Template, data any) (subject, htmlBody string, err error) {
	base, err := template.New("layout").Parse(layoutSrc)
	if err != nil {
		return "", "", fmt.Errorf("parse layout: %w", err)
	}

	page, err := base.Clone()
	if err != nil {
		return "", "", fmt.Errorf("clone layout: %w", err)
	}
	page, err = page.ParseFS(templatesFS, "templates/"+string(tmpl)+".html")
	if err != nil {
		return "", "", fmt.Errorf("parse template %q: %w", tmpl, err)
	}

	var subjectBuf bytes.Buffer
	if err := page.ExecuteTemplate(&subjectBuf, "subject", data); err != nil {
		return "", "", fmt.Errorf("render subject for %q: %w", tmpl, err)
	}
	var bodyBuf bytes.Buffer
	if err := page.ExecuteTemplate(&bodyBuf, "layout", data); err != nil {
		return "", "", fmt.Errorf("render body for %q: %w", tmpl, err)
	}

	return subjectBuf.String(), bodyBuf.String(), nil
}
