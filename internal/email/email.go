// Package email is the host's SMTP client and its emails: password resets
// and notices about an account (created, password, email or groups
// changed). The SMTP settings are the caller's to load (internal/signin
// keeps them in a Secret); this package has no Kubernetes in it.
//
// It never logs: it returns errors, and callers decide how loud to be. A
// notice that fails is logged and the change it reports still stands; a
// test send reports the failure to whoever pressed the button.
package email

import (
	"bytes"
	"context"
	"crypto/tls"
	"embed"
	"errors"
	"fmt"
	"html/template"

	mail "github.com/wneessen/go-mail"
)

// ErrNotConfigured is returned by Send when no SMTP host is set. It's the
// usual state of a fresh install, not a failure: callers check for it.
var ErrNotConfigured = errors.New("email: SMTP isn't configured")

// DefaultFromName is the sender's name when Settings.FromName is empty.
const DefaultFromName = "hyve"

// Settings is how the host sends mail.
type Settings struct {
	Host string
	Port int
	// Username and Password log in to the relay; both empty is an
	// anonymous relay, as a local Postfix often is.
	Username string
	Password string
	// UseTLS is implicit TLS (usually port 465). Without it the connection
	// upgrades with STARTTLS when the server offers it (usually 587).
	UseTLS bool
	// SkipVerify accepts any certificate: only for an internal relay with
	// a self-signed one.
	SkipVerify  bool
	FromAddress string
	FromName    string
}

// Configured reports whether mail can be sent at all.
func (s Settings) Configured() bool { return s.Host != "" }

// Template names one of the emails.
type Template string

const (
	// TemplatePasswordResetCode is the forgot-password email: a code, and
	// a link when the host knows its public address.
	TemplatePasswordResetCode Template = "password-reset-code"
	// TemplatePasswordChanged is sent after any password change.
	TemplatePasswordChanged Template = "password-changed"
	// TemplateGroupsChanged tells a user their groups changed.
	TemplateGroupsChanged Template = "groups-changed"
	// TemplateEmailChanged goes to the old address when it's changed, so
	// whoever owned it hears about it.
	TemplateEmailChanged Template = "email-changed"
	// TemplateAccountCreated tells a new user their account exists. Never
	// with the password.
	TemplateAccountCreated Template = "account-created"
	// TemplateTest checks the SMTP settings work.
	TemplateTest Template = "test"
)

// PasswordResetCodeData is TemplatePasswordResetCode's data. Link may be
// empty: the code alone works.
type PasswordResetCodeData struct {
	Username string
	Code     string
	Link     string
}

// PasswordChangedData is TemplatePasswordChanged's data.
type PasswordChangedData struct {
	Username string
}

// GroupsChangedData is TemplateGroupsChanged's data: each a comma-separated
// list, or "none".
type GroupsChangedData struct {
	Username  string
	OldGroups string
	NewGroups string
}

// EmailChangedData is TemplateEmailChanged's data. NewEmail is empty when
// the address was removed.
type EmailChangedData struct {
	Username string
	NewEmail string
}

// AccountCreatedData is TemplateAccountCreated's data.
type AccountCreatedData struct {
	Username string
	Groups   string
}

// TestEmailData is TemplateTest's data.
type TestEmailData struct {
	RequestedBy string
}

//go:embed templates/*.html
var templatesFS embed.FS

//go:embed templates/layout.html
var layoutSrc string

// render returns tmpl's subject and HTML body. Each email is parsed into
// its own clone of the layout: they all define "subject" and "body", which
// would overwrite each other in one template set.
func render(tmpl Template, data any) (subject, body string, err error) {
	base, err := template.New("layout").Parse(layoutSrc)
	if err != nil {
		return "", "", fmt.Errorf("parse layout: %w", err)
	}
	page, err := base.Clone()
	if err != nil {
		return "", "", err
	}
	if page, err = page.ParseFS(templatesFS, "templates/"+string(tmpl)+".html"); err != nil {
		return "", "", fmt.Errorf("parse template %q: %w", tmpl, err)
	}
	var s, b bytes.Buffer
	if err := page.ExecuteTemplate(&s, "subject", data); err != nil {
		return "", "", fmt.Errorf("render the subject of %q: %w", tmpl, err)
	}
	if err := page.ExecuteTemplate(&b, "layout", data); err != nil {
		return "", "", fmt.Errorf("render %q: %w", tmpl, err)
	}
	return s.String(), b.String(), nil
}

// newClient builds a client for one send: settings can change at any time.
func newClient(s Settings) (*mail.Client, error) {
	port := s.Port
	if port == 0 {
		port = 587
	}
	opts := []mail.Option{mail.WithPort(port)}
	if s.UseTLS {
		opts = append(opts, mail.WithSSL())
	} else {
		// Opportunistic, not mandatory: an internal relay without STARTTLS
		// (Mailpit, a local Postfix) still works.
		opts = append(opts, mail.WithTLSPolicy(mail.TLSOpportunistic))
	}
	if s.SkipVerify {
		// This replaces go-mail's TLS config rather than merging into it,
		// so ServerName and MinVersion are set again.
		opts = append(opts, mail.WithTLSConfig(&tls.Config{
			ServerName:         s.Host,
			MinVersion:         mail.DefaultTLSMinVersion,
			InsecureSkipVerify: true, //nolint:gosec // the admin's choice, for a self-signed internal relay
		}))
	}
	if s.Username != "" && s.Password != "" {
		opts = append(opts,
			mail.WithSMTPAuth(mail.SMTPAuthAutoDiscover),
			mail.WithUsername(s.Username),
			mail.WithPassword(s.Password),
		)
	}
	return mail.NewClient(s.Host, opts...)
}

// Send renders tmpl with data and sends it to "to". It returns
// ErrNotConfigured when settings has no host.
func Send(ctx context.Context, settings Settings, to string, tmpl Template, data any) error {
	if !settings.Configured() {
		return ErrNotConfigured
	}
	subject, body, err := render(tmpl, data)
	if err != nil {
		return err
	}
	c, err := newClient(settings)
	if err != nil {
		return fmt.Errorf("build the SMTP client: %w", err)
	}
	fromName := settings.FromName
	if fromName == "" {
		fromName = DefaultFromName
	}
	msg := mail.NewMsg()
	if err := msg.FromFormat(fromName, settings.FromAddress); err != nil {
		return fmt.Errorf("the from address: %w", err)
	}
	if err := msg.To(to); err != nil {
		return fmt.Errorf("the recipient: %w", err)
	}
	msg.Subject(subject)
	msg.SetBodyString(mail.TypeTextHTML, body)
	if err := c.DialAndSendWithContext(ctx, msg); err != nil {
		return fmt.Errorf("send: %w", err)
	}
	return nil
}
