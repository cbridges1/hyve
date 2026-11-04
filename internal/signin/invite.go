package signin

import (
	"context"
	"errors"
	"log"
	"net/http"

	apierrors "k8s.io/apimachinery/pkg/api/errors"

	"github.com/cbridges1/hyve/internal/email"
)

// InviteAnnotation on a new User says an invitation is coming (POST
// /v1/users/{name}/invite), so the plain "account created" email isn't
// sent as well. It only holds that email back: sending the invitation
// takes the right to set the user's password.
const InviteAnnotation = "hyve.io/invite"

// handleInvite emails a user a link and code to set their own password:
// how a new user is onboarded without anyone else choosing their password,
// and how an invitation is sent again. It takes what setting the user's
// password takes (update on users/password), since the code does that.
// Unlike a reset request, it reports failures: whoever sends it is waiting
// to know it went.
func (s *Server) handleInvite(w http.ResponseWriter, r *http.Request) {
	ident, ok := s.authenticate(w, r)
	if !ok {
		return
	}
	if s.NoPasswords {
		writeError(w, http.StatusForbidden, "PasswordsDisabled", "password sign-in is turned off on this host; users sign in through single sign-on")
		return
	}
	name := r.PathValue("name")
	allowed, err := s.canSetPassword(r.Context(), ident, name)
	if err != nil {
		log.Printf("signin: access review for %s inviting %s: %v", ident.Username, name, err)
		writeError(w, http.StatusInternalServerError, "ServerError", "couldn't check permissions")
		return
	}
	if !allowed {
		writeError(w, http.StatusForbidden, "Forbidden", "you can't set this user's password, so you can't invite them")
		return
	}
	u, err := s.Store.getUser(r.Context(), name)
	switch {
	case apierrors.IsNotFound(err):
		writeError(w, http.StatusNotFound, "NotFound", "no such user")
		return
	case err != nil:
		log.Printf("signin: inviting %s: %v", name, err)
		writeError(w, http.StatusInternalServerError, "ServerError", "couldn't read the user")
		return
	case u.Spec.Provider != "":
		writeError(w, http.StatusBadRequest, "InvalidFormat", "this user signs in through the identity provider and has no password to set")
		return
	case u.Spec.Disabled:
		writeError(w, http.StatusBadRequest, "InvalidFormat", "this user is disabled")
		return
	case u.Spec.Email == "":
		writeError(w, http.StatusBadRequest, "InvalidFormat", "this user has no email address to invite")
		return
	}
	settings, err := s.Store.EmailSettings(r.Context())
	if err != nil {
		log.Printf("signin: reading the email settings: %v", err)
		writeError(w, http.StatusInternalServerError, "ServerError", "couldn't read the email settings")
		return
	}
	if !settings.Configured() {
		writeError(w, http.StatusBadRequest, "NotConfigured", "email isn't set up, so invitations can't be sent: set it up in Settings, or set a password instead")
		return
	}
	code, err := s.Store.NewResetCode(r.Context(), u, InviteTTL)
	if err != nil {
		log.Printf("signin: inviting %s: %v", name, err)
		writeError(w, http.StatusInternalServerError, "ServerError", "couldn't create the invitation")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), mailTimeout)
	defer cancel()
	err = s.sendMail(ctx, u.Spec.Email, email.TemplateInvite, email.InviteData{
		Username:  name,
		Groups:    groupList(sortedGroups(u.Spec.Groups)),
		Code:      code,
		Link:      s.resetLink(name, code, true),
		ExpiresIn: "7 days",
	})
	if err != nil && !errors.Is(err, email.ErrNotConfigured) {
		writeError(w, http.StatusBadGateway, "SendFailed", "the invitation email failed: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"sent": true})
}
