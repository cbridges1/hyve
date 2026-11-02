package signin

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"log"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	hyvev1alpha1 "github.com/cbridges1/hyve/internal/apis/hyve/v1alpha1"
	"github.com/cbridges1/hyve/internal/email"
)

const (
	// ResetCodeTTL is how long a password-reset code works.
	ResetCodeTTL = 2 * time.Hour

	labelResetFor = "hyve.io/reset-for"
	resetAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	resetLength   = 8
)

// ErrInvalidResetCode covers an unknown user, a wrong code and an expired
// one alike.
var ErrInvalidResetCode = errors.New("invalid or expired reset code")

func resetSecretName(username string) string { return "hyve-reset-" + username }

func newResetCode() (string, error) {
	b := make([]byte, resetLength)
	for i := range b {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(resetAlphabet))))
		if err != nil {
			return "", err
		}
		b[i] = resetAlphabet[n.Int64()]
	}
	return string(b), nil
}

// findResetUser returns the password User a username or email address
// names, or nil: an identity provider's users have no password to reset,
// and a disabled user can't sign in anyway.
func (s *Store) findResetUser(ctx context.Context, identifier string) (*hyvev1alpha1.User, error) {
	identifier = strings.TrimSpace(identifier)
	var u *hyvev1alpha1.User
	if strings.Contains(identifier, "@") {
		var list hyvev1alpha1.UserList
		if err := s.Client.List(ctx, &list); err != nil {
			return nil, err
		}
		for i := range list.Items {
			if strings.EqualFold(list.Items[i].Spec.Email, identifier) {
				u = &list.Items[i]
				break
			}
		}
	} else if ValidUsername(identifier) {
		got, err := s.getUser(ctx, identifier)
		if apierrors.IsNotFound(err) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		u = got
	}
	if u == nil || u.Spec.Provider != "" || u.Spec.Disabled {
		return nil, nil
	}
	return u, nil
}

// NewResetCode starts a password reset for u: a code, kept as a bcrypt hash
// in a Secret owned by u, that replaces any earlier one.
func (s *Store) NewResetCode(ctx context.Context, u *hyvev1alpha1.User) (string, error) {
	code, err := newResetCode()
	if err != nil {
		return "", err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(code), bcryptCost)
	if err != nil {
		return "", err
	}
	data := map[string][]byte{keyHash: hash, keyExpiresAt: []byte(s.now().Add(ResetCodeTTL).Format(time.RFC3339))}
	sec := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: s.Namespace, Name: resetSecretName(u.Name)}}
	err = s.Client.Get(ctx, client.ObjectKeyFromObject(sec), sec)
	switch {
	case apierrors.IsNotFound(err):
		sec.Labels = map[string]string{labelResetFor: u.Name}
		sec.OwnerReferences = []metav1.OwnerReference{ownerRef(u)}
		sec.Type = corev1.SecretTypeOpaque
		sec.Data = data
		err = s.Client.Create(ctx, sec)
	case err == nil:
		sec.Data = data
		err = s.Client.Update(ctx, sec)
	}
	return code, err
}

// ResetPassword sets u's password if code is its live reset code, uses the
// code up, and signs the user out everywhere.
func (s *Store) ResetPassword(ctx context.Context, u *hyvev1alpha1.User, code, newPassword string) error {
	var sec corev1.Secret
	err := s.Client.Get(ctx, client.ObjectKey{Namespace: s.Namespace, Name: resetSecretName(u.Name)}, &sec)
	if apierrors.IsNotFound(err) {
		_ = bcrypt.CompareHashAndPassword(dummyHash(), []byte(code))
		return ErrInvalidResetCode
	}
	if err != nil {
		return err
	}
	expiresAt, err := time.Parse(time.RFC3339, string(sec.Data[keyExpiresAt]))
	if err != nil || !s.now().Before(expiresAt) {
		return ErrInvalidResetCode
	}
	if bcrypt.CompareHashAndPassword(sec.Data[keyHash], []byte(strings.ToUpper(strings.TrimSpace(code)))) != nil {
		return ErrInvalidResetCode
	}
	if err := ValidatePassword(newPassword); err != nil {
		return errors.Join(ErrInvalidNewPassword, err)
	}
	if err := s.SetPassword(ctx, u.Name, newPassword); err != nil {
		return err
	}
	if err := client.IgnoreNotFound(s.Client.Delete(ctx, &sec)); err != nil {
		log.Printf("signin: deleting %s's used reset code: %v", u.Name, err)
	}
	if err := s.RevokeSessions(ctx, u.Name, ""); err != nil {
		log.Printf("signin: revoking %s's sessions after a reset: %v", u.Name, err)
	}
	return nil
}

// DeleteExpiredResetCodes removes reset codes past their expiry.
func (s *Store) DeleteExpiredResetCodes(ctx context.Context) error {
	var list corev1.SecretList
	if err := s.Client.List(ctx, &list, client.InNamespace(s.Namespace), client.HasLabels{labelResetFor}); err != nil {
		return err
	}
	for i := range list.Items {
		sec := &list.Items[i]
		expiresAt, err := time.Parse(time.RFC3339, string(sec.Data[keyExpiresAt]))
		if err == nil && s.now().Before(expiresAt) {
			continue
		}
		if err := client.IgnoreNotFound(s.Client.Delete(ctx, sec)); err != nil {
			return err
		}
	}
	return nil
}

// resetDelay is randomDelay; tests replace it.
var resetDelay = randomDelay

// randomDelay sleeps up to two seconds, so "no such user" and "email sent"
// take about as long.
func randomDelay(ctx context.Context) {
	n, err := rand.Int(rand.Reader, big.NewInt(2000))
	if err != nil {
		return
	}
	select {
	case <-time.After(time.Duration(n.Int64()) * time.Millisecond):
	case <-ctx.Done():
	}
}

// ResetRequest is POST /v1-public/password-reset.
type ResetRequest struct {
	// Identifier is a username or an email address.
	Identifier string `json:"identifier"`
}

// ResetConfirm is POST /v1-public/password-reset/confirm.
type ResetConfirm struct {
	// Identifier is a username or an email address, as in the request.
	Identifier  string `json:"identifier"`
	Code        string `json:"code"`
	NewPassword string `json:"newPassword"`
}

// resetLink is where the email's button goes: the UI's reset page, on the
// host's configured public address. Never the request's Host header, which
// whoever asks for the reset controls. Empty without one: the code alone
// works.
func (s *Server) resetLink(username, code string) string {
	if s.PublicURL == "" {
		return ""
	}
	q := url.Values{"user": {username}, "code": {code}}
	return strings.TrimRight(s.PublicURL, "/") + "/reset-password?" + q.Encode()
}

// handleResetRequest emails a reset code to the user a username or address
// names. It always answers the same way, after a random delay, so it can't
// tell anyone which users exist or have an address. Without SMTP the code
// goes to the host's log, so a fresh install's admin can still get in.
func (s *Server) handleResetRequest(w http.ResponseWriter, r *http.Request) {
	if s.NoPasswords {
		writeError(w, http.StatusForbidden, "PasswordsDisabled", "password sign-in is turned off on this host; use single sign-on")
		return
	}
	var req ResetRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req); err != nil || strings.TrimSpace(req.Identifier) == "" {
		writeError(w, http.StatusBadRequest, "InvalidBodyContent", "a username or email address is needed")
		return
	}
	addrKey := "reset:" + clientAddr(r)
	if s.addrLimit.Blocked(addrKey) {
		writeError(w, http.StatusTooManyRequests, "TooManyRequests", "too many requests; try again later")
		return
	}
	// Each request counts: it sends mail.
	s.addrLimit.Fail(addrKey)

	ctx := r.Context()
	defer resetDelay(ctx)
	u, err := s.Store.findResetUser(ctx, req.Identifier)
	if err != nil {
		log.Printf("signin: password reset for %q: %v", req.Identifier, err)
	}
	if u == nil || u.Spec.Email == "" {
		writeJSON(w, http.StatusOK, map[string]bool{"sent": true})
		return
	}
	code, err := s.Store.NewResetCode(ctx, u)
	if err != nil {
		log.Printf("signin: password reset for %s: %v", u.Name, err)
		writeError(w, http.StatusInternalServerError, "ServerError", "couldn't start the password reset")
		return
	}
	to, name, link := u.Spec.Email, u.Name, s.resetLink(u.Name, code)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), mailTimeout)
		defer cancel()
		err := s.sendMail(ctx, to, email.TemplatePasswordResetCode, email.PasswordResetCodeData{Username: name, Code: code, Link: link})
		switch {
		case errors.Is(err, email.ErrNotConfigured):
			log.Printf("signin: password reset requested for %s, but email isn't configured; the code is %s", name, code)
		case err != nil:
			log.Printf("signin: sending %s's password reset to %s: %v", name, to, err)
		}
	}()
	writeJSON(w, http.StatusOK, map[string]bool{"sent": true})
}

// handleResetConfirm sets a new password with a reset code. Wrong codes
// count against the same limits as wrong passwords.
func (s *Server) handleResetConfirm(w http.ResponseWriter, r *http.Request) {
	if s.NoPasswords {
		writeError(w, http.StatusForbidden, "PasswordsDisabled", "password sign-in is turned off on this host; use single sign-on")
		return
	}
	var req ResetConfirm
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "InvalidBodyContent", "the request body isn't valid JSON")
		return
	}
	if req.Identifier == "" || req.Code == "" || req.NewPassword == "" {
		writeError(w, http.StatusBadRequest, "InvalidBodyContent", "identifier, code and newPassword are needed")
		return
	}
	userKey, addrKey := "user:"+strings.ToLower(req.Identifier), "addr:"+clientAddr(r)
	if s.userLimit.Blocked(userKey) || s.addrLimit.Blocked(addrKey) {
		writeError(w, http.StatusTooManyRequests, "TooManyRequests", "too many failed attempts; try again later")
		return
	}
	if err := ValidatePassword(req.NewPassword); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "InvalidPassword", err.Error())
		return
	}
	ctx := r.Context()
	u, err := s.Store.findResetUser(ctx, req.Identifier)
	if err == nil && u == nil {
		err = ErrInvalidResetCode
	}
	if err == nil {
		err = s.Store.ResetPassword(ctx, u, req.Code, req.NewPassword)
	}
	switch {
	case errors.Is(err, ErrInvalidResetCode):
		s.userLimit.Fail(userKey)
		s.addrLimit.Fail(addrKey)
		writeError(w, http.StatusBadRequest, "InvalidResetCode", "the reset code is wrong or has expired")
		return
	case errors.Is(err, ErrInvalidNewPassword):
		writeError(w, http.StatusUnprocessableEntity, "InvalidPassword", err.Error())
		return
	case err != nil:
		log.Printf("signin: password reset for %q: %v", req.Identifier, err)
		writeError(w, http.StatusInternalServerError, "ServerError", "couldn't reset the password")
		return
	}
	s.userLimit.Succeed(userKey)
	s.notify(u.Spec.Email, email.TemplatePasswordChanged, email.PasswordChangedData{Username: u.Name})
	writeJSON(w, http.StatusOK, map[string]bool{"reset": true})
}
