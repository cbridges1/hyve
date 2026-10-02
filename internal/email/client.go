// Package email is hyve-api's own SMTP client + template rendering —
// password-reset and account-notification mail, see
// HYVE-EMAIL-IMPLEMENTATION-PLAN.md (nexus-config/docs) for the full
// design. Deliberately has no Kubernetes dependency anywhere in this
// package: its configuration (orgdb.EmailSettings) lives in hyve-api's
// own Postgres/SQLite datastore, not a Kubernetes CRD/Secret, so sending
// mail works identically whether or not this install has a home cluster
// of its own (--home-cluster=none) — the same reasoning that already put
// reconciling-cluster kubeconfigs in orgdb instead of Kubernetes Secrets.
//
// This package never logs — every function here returns the real error
// and leaves the decision of what to do with it (log-and-continue for a
// best-effort notification, surface it to an API caller for a test send)
// to internal/api, the same "packages return errors, callers decide how
// loud to be about them" shape every other internal/* package in this
// repo already follows.
package email

import (
	"crypto/tls"
	"errors"

	mail "github.com/wneessen/go-mail"

	"github.com/cbridges1/hyve/internal/orgdb"
)

// ErrNotConfigured is returned by Send when no SMTP host has ever been
// saved — orgdb.EmailSettings.Configured() == false. Not an error
// condition on a fresh install; every caller in internal/api is expected
// to check for it (errors.Is) and react accordingly — log a fallback for
// a password-reset token, for instance — rather than treating it as a
// genuine failure.
var ErrNotConfigured = errors.New("email: smtp is not configured")

// newClient builds a go-mail Client from settings, freshly on every call.
// Unlike a typical long-lived SMTP transport, this is deliberately not
// cached across sends: settings are runtime-editable via
// PATCH /api/system/email (Milestone 3), so there's no point in the
// process lifetime a cached client is guaranteed still valid, and the
// TCP+TLS handshake this incurs per send is not the bottleneck for a
// low-volume transactional mailer like this one.
func newClient(settings orgdb.EmailSettings) (*mail.Client, error) {
	opts := []mail.Option{
		mail.WithPort(settings.SMTPPort),
	}

	if settings.UseTLS {
		opts = append(opts, mail.WithSSL())
	} else {
		// Opportunistic STARTTLS, not mandatory — upgrades to an
		// encrypted connection when the server offers it, same as any
		// real relay (Postfix, SES, Gmail) does, but doesn't hard-fail
		// when it doesn't. Matches Pangolin's own actual behavior
		// (nodemailer's secure: false already works this way, see
		// sendEmail.ts) and is what real self-hosted plaintext-relay
		// scenarios need — confirmed live: TLSMandatory here rejected an
		// internal-network Mailpit sink outright ("target host does not
		// support STARTTLS"), which is exactly the kind of local/internal
		// relay a self-hosted admin legitimately points this at.
		opts = append(opts, mail.WithTLSPolicy(mail.TLSOpportunistic))
	}

	if settings.SkipVerify {
		// WithTLSConfig fully replaces go-mail's own default tls.Config
		// (see its own doc comment: "overrides the default
		// configuration") rather than merging into it, so ServerName and
		// MinVersion have to be restated here — otherwise SNI silently
		// breaks along with cert validation.
		opts = append(opts, mail.WithTLSConfig(&tls.Config{
			ServerName:         settings.SMTPHost,
			MinVersion:         mail.DefaultTLSMinVersion,
			InsecureSkipVerify: true,
		}))
	}

	if settings.SMTPUsername != nil && settings.SMTPPassword != nil {
		opts = append(opts,
			mail.WithSMTPAuth(mail.SMTPAuthAutoDiscover),
			mail.WithUsername(*settings.SMTPUsername),
			mail.WithPassword(*settings.SMTPPassword),
		)
	}
	// No username/password at all is a deliberate, valid configuration —
	// an anonymous relay reachable only from hyve-api's own network,
	// which some self-hosted setups (a local Postfix, an internal relay)
	// genuinely use. go-mail defaults to no auth when WithSMTPAuth is
	// never called.

	return mail.NewClient(settings.SMTPHost, opts...)
}
