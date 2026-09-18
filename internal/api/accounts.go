package api

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"

	hyvev1alpha1 "github.com/cbridges1/hyve/internal/apis/hyve/v1alpha1"
	"github.com/cbridges1/hyve/internal/email"
	"github.com/cbridges1/hyve/internal/orgdb"
)

// sendAccountNotification is the shared fire-and-forget wrapper every
// account-lifecycle notification in this file goes through: the
// underlying account action has already succeeded by the time this is
// called, so a delivery failure is logged, never turned into a failed API
// response — see HYVE-EMAIL-IMPLEMENTATION-PLAN.md's Milestone 5 for why
// every one of these call sites takes this same stance. ErrNotConfigured
// (no SMTP set up) isn't even worth logging — it's the expected, common
// state for a fresh self-hosted install, not a failure.
func sendAccountNotification(s *Server, r *http.Request, to string, tmpl email.Template, data any) {
	if err := email.Send(r.Context(), s.OrgStore, to, tmpl, data); err != nil && !errors.Is(err, email.ErrNotConfigured) {
		log.Printf("api: failed to send %q notification to %q: %v", tmpl, to, err)
	}
}

// accountDTO is the response shape for GET /api/accounts — deliberately
// carries no password hash (this never even reads the paired credentials
// Secret, only the binding). Local accounts only: an OIDC-subject binding
// has nothing here to "manage" — no password to reset, no Secret to
// delete — so it's filtered out rather than shown half-functional.
type accountDTO struct {
	Username string  `json:"username"`
	Role     string  `json:"role"`
	Email    *string `json:"email,omitempty"`
}

func toAccountDTO(b orgdb.Binding) accountDTO {
	return accountDTO{Username: b.Identity, Role: b.Role, Email: b.Email}
}

// registerAccountRoutes wires /accounts — mounted under /api/ (behind
// requireAuth+requireRole) by Server.Routes. Every handler additionally
// gates RoleAdmin: account management is inherently an admin-only concern,
// same precedent as clusters/templates/workflows/resources' own
// create/delete.
func (s *Server) registerAccountRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /accounts", s.handleListAccounts)
	mux.HandleFunc("POST /accounts", s.handleCreateAccount)
	mux.HandleFunc("GET /accounts/{username}", s.handleGetAccount)
	mux.HandleFunc("PATCH /accounts/{username}", s.handleUpdateAccount)
	mux.HandleFunc("DELETE /accounts/{username}", s.handleDeleteAccount)
	mux.HandleFunc("PUT /accounts/{username}/password", s.handleUpdateAccountPassword)
}

// handleGetAccount backs the console's per-user detail page — a single-
// resource fetch alongside the existing list, same shape every other
// resource type in this API already has (GET /clusters/{name} etc.).
// Same visibility rule as list/delete/update: a superadmin account is
// reported as not-found to a non-superadmin caller, not forbidden.
func (s *Server) handleGetAccount(w http.ResponseWriter, r *http.Request) {
	if !RequireRole(w, r, hyvev1alpha1.RoleAdmin, hyvev1alpha1.RoleSuperadmin) {
		return
	}
	username := r.PathValue("username")
	callerRole, _ := RoleFromContext(r.Context())

	binding, err := s.findBindingBySubject(r.Context(), s.TenantNamespace(r), orgdb.SubjectTypeLocal, username)
	if err != nil {
		writeError(w, http.StatusNotFound, "account not found")
		return
	}
	if binding.Role == hyvev1alpha1.RoleSuperadmin && callerRole != hyvev1alpha1.RoleSuperadmin {
		writeError(w, http.StatusNotFound, "account not found")
		return
	}
	writeJSON(w, http.StatusOK, toAccountDTO(binding))
}

// validateAndCheckEmail is the shared email-handling logic between account
// creation and update: light syntactic validation (this isn't the
// platform's verification step, just enough to catch an obvious typo) plus
// the per-namespace uniqueness check (see
// migrations/*/0003_binding_email.sql). email == "" means "no email" and
// always passes through as nil with no lookup. excludeBindingID skips that
// one row's own match (an update finding only itself isn't a conflict);
// pass "" from account creation, which has no existing row to exclude.
func (s *Server) validateAndCheckEmail(w http.ResponseWriter, r *http.Request, namespace, email, excludeBindingID string) (*string, bool) {
	email = strings.TrimSpace(email)
	if email == "" {
		return nil, true
	}
	if !strings.Contains(email, "@") {
		writeError(w, http.StatusBadRequest, "invalid email address")
		return nil, false
	}
	existing, err := s.OrgStore.FindBindingByEmail(r.Context(), namespace, email)
	if err == nil && existing.ID != excludeBindingID {
		writeError(w, http.StatusConflict, "an account with this email already exists")
		return nil, false
	}
	if err != nil && err != orgdb.ErrNotFound {
		log.Printf("api: failed to check existing email %q: %v", email, err)
		writeError(w, http.StatusInternalServerError, "failed to save account")
		return nil, false
	}
	return &email, true
}

func (s *Server) handleListAccounts(w http.ResponseWriter, r *http.Request) {
	if !RequireRole(w, r, hyvev1alpha1.RoleAdmin, hyvev1alpha1.RoleSuperadmin) {
		return
	}
	callerRole, _ := RoleFromContext(r.Context())
	bindings, err := s.OrgStore.ListBindingsForScope(r.Context(), s.TenantNamespace(r))
	if err != nil {
		log.Printf("api: failed to list access bindings: %v", err)
		writeError(w, http.StatusInternalServerError, "failed to list accounts")
		return
	}
	dtos := make([]accountDTO, 0, len(bindings))
	for _, b := range bindings {
		if b.SubjectType != orgdb.SubjectTypeLocal {
			continue
		}
		// A superadmin binding is never visible to an ordinary admin, even
		// one sharing its scope (the control plane, where both kinds of
		// binding live) — confirmed live: without this, any tenant admin
		// could see AND delete a superadmin account purely by co-locating
		// in the same namespace. Superadmin privilege is cluster-wide;
		// only another superadmin gets to see or manage it.
		if b.Role == hyvev1alpha1.RoleSuperadmin && callerRole != hyvev1alpha1.RoleSuperadmin {
			continue
		}
		dtos = append(dtos, toAccountDTO(b))
	}
	writeJSON(w, http.StatusOK, dtos)
}

type createAccountRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Role     string `json:"role"`

	// Email is optional at creation, same as it is on PATCH — see
	// validateAndCheckEmail and orgdb.Binding.Email's own doc comment.
	Email string `json:"email,omitempty"`

	// Namespace lets a superadmin caller target a tenant namespace other
	// than their own (they have none — see RoleSuperadmin's doc comment)
	// — e.g. creating a brand new tenant's first admin right after POST
	// /organizations. Ignored for a non-superadmin caller, who is always
	// confined to their own session's namespace regardless of what this
	// field says, same as before this field existed.
	Namespace string `json:"namespace,omitempty"`

	// Environment scopes this grant within the target organization —
	// omitted resolves to that organization's own environment when it has
	// exactly one (see resolveResourceEnvironment's identical rule for the
	// four resource types). Ignored for role=superadmin, which has no
	// organization/environment at all.
	Environment string `json:"environment,omitempty"`
}

// handleCreateAccount supports admin/read-only (both fixed, well-known
// ServiceAccounts — hyve-access-admin/hyve-access-readonly, see
// deploy/helm/hyve's api-access-roles.yaml, or POST /organizations' own
// programmatic equivalent for a shared install) and, for a superadmin
// caller only, superadmin itself — a role can already create more of its
// own role everywhere else in this system (an admin already creates more
// admins), so a superadmin creating another superadmin isn't a new kind of
// escalation, just the same principle at the top tier. A `custom` role
// needs an operator-defined ServiceAccount this endpoint has no field
// for, and still goes through `hyve cluster-config api create-user --role
// custom --service-account <name>`, unchanged.
func (s *Server) handleCreateAccount(w http.ResponseWriter, r *http.Request) {
	if !RequireRole(w, r, hyvev1alpha1.RoleAdmin, hyvev1alpha1.RoleSuperadmin) {
		return
	}
	var req createAccountRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Username == "" || req.Password == "" {
		writeError(w, http.StatusBadRequest, "username and password are required")
		return
	}

	callerRole, _ := RoleFromContext(r.Context())

	if req.Role == hyvev1alpha1.RoleSuperadmin && callerRole != hyvev1alpha1.RoleSuperadmin {
		writeError(w, http.StatusForbidden, "only a superadmin can create another superadmin")
		return
	}

	var serviceAccount string
	switch req.Role {
	case hyvev1alpha1.RoleAdmin, hyvev1alpha1.RoleSuperadmin, hyvev1alpha1.RoleReadOnly:
		serviceAccount = orgdb.ServiceAccountNameForRole(req.Role)
	default:
		writeError(w, http.StatusBadRequest, `role must be "admin", "read-only", or "superadmin" (a custom role needs 'hyve cluster-config api create-user --role custom' instead)`)
		return
	}

	ctx := r.Context()

	// A new superadmin's binding has no organization/environment at all —
	// that's the one place login ever looks for it (a superadmin has no
	// tenant of their own), and the one place the list/delete handlers
	// above already expect to find it. Its paired credentials Secret still
	// needs a real namespace to live in, which stays s.Namespace (the
	// control-plane namespace) exactly as before — overriding both
	// req.Namespace and whatever the caller's own "act as" selection
	// currently is, confirmed live this matters: creating a superadmin
	// while "Viewing" some tenant would otherwise silently create a
	// credentials Secret nothing can ever find.
	var (
		secretNamespace string
		organizationID  *string
		environmentID   *string
	)
	if req.Role == hyvev1alpha1.RoleSuperadmin {
		secretNamespace = s.Namespace
	} else {
		ns := s.TenantNamespace(r)
		if callerRole == hyvev1alpha1.RoleSuperadmin && req.Namespace != "" {
			ns = req.Namespace
		}
		secretNamespace = ns

		// No Organization for ns at all is not an error — it's a
		// self-hosted, single-tenant install that never ran POST
		// /organizations (see resolveResourceEnvironment's own doc
		// comment), and this binding lands with organizationID/
		// environmentID both nil, exactly like a superadmin's, rather than
		// being rejected. Only when an Organization genuinely exists does
		// environment resolution apply at all.
		env, ok, envErr := s.resolveResourceEnvironment(ctx, ns, req.Environment)
		if envErr != nil {
			status := http.StatusBadRequest
			if !ok {
				status = http.StatusInternalServerError
			}
			writeError(w, status, envErr.Error())
			return
		}
		if ok {
			organizationID = &env.OrganizationID
			environmentID = &env.ID
		}
	}

	if _, err := s.findBindingBySubject(ctx, secretNamespace, orgdb.SubjectTypeLocal, req.Username); err == nil {
		writeError(w, http.StatusConflict, "an account with this username already exists")
		return
	} else if err != orgdb.ErrNotFound {
		log.Printf("api: failed to check existing account %q: %v", req.Username, err)
		writeError(w, http.StatusInternalServerError, "failed to create account")
		return
	}

	hash, err := HashPassword(req.Password)
	if err != nil {
		log.Printf("api: failed to hash password for new account %q: %v", req.Username, err)
		writeError(w, http.StatusInternalServerError, "failed to create account")
		return
	}

	accountEmail, ok := s.validateAndCheckEmail(w, r, secretNamespace, req.Email, "")
	if !ok {
		return
	}

	binding, err := s.OrgStore.CreateBinding(ctx, orgdb.Binding{
		Namespace:               secretNamespace,
		OrganizationID:          organizationID,
		EnvironmentID:           environmentID,
		SubjectType:             orgdb.SubjectTypeLocal,
		Identity:                req.Username,
		Role:                    req.Role,
		Email:                   accountEmail,
		ServiceAccountName:      serviceAccount,
		ServiceAccountNamespace: secretNamespace,
		PasswordHash:            &hash,
	})
	if err != nil {
		log.Printf("api: failed to create access binding for %q: %v", req.Username, err)
		writeError(w, http.StatusInternalServerError, "failed to create account")
		return
	}

	if binding.Email != nil {
		sendAccountNotification(s, r, *binding.Email, email.TemplateAccountCreated, email.AccountCreatedData{
			Username: binding.Identity,
			Role:     binding.Role,
		})
	}

	writeJSON(w, http.StatusCreated, toAccountDTO(binding))
}

// updateAccountRequest's Role/Email are both nil-means-"leave unchanged" —
// a plain string can't distinguish that from "clear this field," which
// Email in particular needs (an admin removing an account's email).
type updateAccountRequest struct {
	Role  *string `json:"role,omitempty"`
	Email *string `json:"email,omitempty"`

	// Namespace is the destination tenant when Role moves a binding OUT of
	// superadmin, which has no tenant of its own to fall back to — same
	// "superadmin caller can target a tenant namespace" field
	// createAccountRequest.Namespace already has, required only in that one
	// transition. Every other role change stays within the binding's
	// current namespace, so this is ignored otherwise.
	Namespace string `json:"namespace,omitempty"`

	// Environment scopes the grant within Namespace's organization when
	// demoting a superadmin — same resolution rule as
	// createAccountRequest.Environment.
	Environment string `json:"environment,omitempty"`
}

// handleUpdateAccount changes an existing account's role and/or email —
// promote/demote between read-only ("regular" in the console),
// admin, and superadmin, plus tracking a contact email (groundwork for the
// platform's move toward email-driven flows — see orgdb.Binding.Email's
// own doc comment).
//
// A role change that doesn't cross the superadmin boundary (read-only <->
// admin, the common case) is a same-namespace, same-organization/
// environment in-place update: only role and its paired ServiceAccount
// name change. Crossing the boundary in either direction is different in
// kind, not just degree — superadmin bindings live in the control-plane
// namespace with no organization/environment at all (see RoleSuperadmin's
// doc comment) — so it's gated to a superadmin caller and, when demoting
// OUT of superadmin, requires an explicit destination Namespace (there is
// no "current tenant" to infer one from).
func (s *Server) handleUpdateAccount(w http.ResponseWriter, r *http.Request) {
	if !RequireRole(w, r, hyvev1alpha1.RoleAdmin, hyvev1alpha1.RoleSuperadmin) {
		return
	}
	username := r.PathValue("username")
	ctx := r.Context()

	var req updateAccountRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Role == nil && req.Email == nil {
		writeError(w, http.StatusBadRequest, "role or email is required")
		return
	}

	callerRole, _ := RoleFromContext(ctx)

	// A superadmin binding always lives in s.Namespace (see
	// handleCreateAccount) — this lookup finds it there regardless of "act
	// as", the same way handleUpdateAccountPassword's self-change branch
	// does, except here it applies to every caller: a plain admin's own
	// TenantNamespace is already fixed to their one namespace regardless of
	// role, so s.TenantNamespace(r) is the right scope to search in either
	// case.
	ns := s.TenantNamespace(r)
	binding, err := s.findBindingBySubject(ctx, ns, orgdb.SubjectTypeLocal, username)
	if err != nil {
		writeError(w, http.StatusNotFound, "account not found")
		return
	}
	// Same visibility rule as list/delete/password-reset: an ordinary
	// admin can't even see a superadmin account to attempt an edit on it.
	if binding.Role == hyvev1alpha1.RoleSuperadmin && callerRole != hyvev1alpha1.RoleSuperadmin {
		writeError(w, http.StatusNotFound, "account not found")
		return
	}

	oldRole := binding.Role
	oldEmail := binding.Email

	if req.Role != nil {
		if caller, ok := UsernameFromContext(ctx); ok && caller == username {
			writeError(w, http.StatusBadRequest, "cannot change your own role")
			return
		}
		newRole := *req.Role
		switch newRole {
		case hyvev1alpha1.RoleAdmin, hyvev1alpha1.RoleReadOnly, hyvev1alpha1.RoleSuperadmin:
		default:
			writeError(w, http.StatusBadRequest, `role must be "admin", "read-only", or "superadmin"`)
			return
		}

		crossesSuperadmin := newRole == hyvev1alpha1.RoleSuperadmin || binding.Role == hyvev1alpha1.RoleSuperadmin
		if crossesSuperadmin && callerRole != hyvev1alpha1.RoleSuperadmin {
			writeError(w, http.StatusForbidden, "only a superadmin can grant or revoke superadmin")
			return
		}

		targetNamespace := binding.Namespace
		var organizationID, environmentID *string
		switch {
		case newRole == hyvev1alpha1.RoleSuperadmin:
			// Unambiguous destination regardless of where the binding came
			// from — a superadmin's home is always the control plane.
			targetNamespace = s.Namespace
		case binding.Role == hyvev1alpha1.RoleSuperadmin:
			// Demoting out of superadmin: there's no "current tenant" to
			// fall back to, so the caller must say where this binding
			// lands.
			if req.Namespace == "" {
				writeError(w, http.StatusBadRequest, "namespace is required when changing a superadmin's role")
				return
			}
			targetNamespace = req.Namespace
			env, ok, envErr := s.resolveResourceEnvironment(ctx, targetNamespace, req.Environment)
			if envErr != nil {
				status := http.StatusBadRequest
				if !ok {
					status = http.StatusInternalServerError
				}
				writeError(w, status, envErr.Error())
				return
			}
			if ok {
				organizationID = &env.OrganizationID
				environmentID = &env.ID
			}
		default:
			// Plain read-only <-> admin: same namespace/organization/
			// environment the binding already has.
			organizationID = binding.OrganizationID
			environmentID = binding.EnvironmentID
		}

		if targetNamespace != binding.Namespace {
			if _, err := s.findBindingBySubject(ctx, targetNamespace, orgdb.SubjectTypeLocal, username); err == nil {
				writeError(w, http.StatusConflict, "an account with this username already exists in the destination namespace")
				return
			} else if err != orgdb.ErrNotFound {
				log.Printf("api: failed to check existing account %q in %q: %v", username, targetNamespace, err)
				writeError(w, http.StatusInternalServerError, "failed to update account")
				return
			}
		}

		binding.Role = newRole
		binding.ServiceAccountName = orgdb.ServiceAccountNameForRole(newRole)
		binding.Namespace = targetNamespace
		binding.ServiceAccountNamespace = targetNamespace
		binding.OrganizationID = organizationID
		binding.EnvironmentID = environmentID
	}

	if req.Email != nil {
		email, ok := s.validateAndCheckEmail(w, r, binding.Namespace, *req.Email, binding.ID)
		if !ok {
			return
		}
		binding.Email = email
	}

	updated, err := s.OrgStore.UpdateBinding(ctx, binding)
	if err != nil {
		log.Printf("api: failed to update account %q: %v", username, err)
		writeError(w, http.StatusInternalServerError, "failed to update account")
		return
	}

	// Notify the affected account's own holder, not the caller — they're
	// who needs to know their access or contact address just changed
	// (see sendAccountNotification's own doc comment for the fire-and-
	// forget stance every one of these takes).
	if updated.Role != oldRole && updated.Email != nil {
		sendAccountNotification(s, r, *updated.Email, email.TemplateRoleChanged, email.RoleChangedData{
			Username: updated.Identity,
			OldRole:  oldRole,
			NewRole:  updated.Role,
		})
	}
	// Sent to the OLD address, not the new one — a classic account-
	// takeover guard: whoever owned the old address still hears about the
	// change, at an address that can no longer silently redirect future
	// resets. Nothing to send if there was no old address (first time
	// setting one — nothing to protect yet).
	if oldEmail != nil && (updated.Email == nil || *updated.Email != *oldEmail) {
		sendAccountNotification(s, r, *oldEmail, email.TemplateEmailChanged, email.EmailChangedData{
			Username: updated.Identity,
			NewEmail: derefOrEmpty(updated.Email),
		})
	}

	writeJSON(w, http.StatusOK, toAccountDTO(updated))
}

// derefOrEmpty is EmailChangedData.NewEmail's own "cleared, not replaced"
// case — an account's email can be removed entirely via PATCH
// {"email": ""} (see validateAndCheckEmail), and the old-address holder
// still deserves the same security notice when that happens, just with
// nothing to name as the new value.
func derefOrEmpty(s *string) string {
	if s == nil {
		return "(removed)"
	}
	return *s
}

// handleDeleteAccount refuses to let a caller delete their own account —
// not a security boundary (an admin could always create another admin
// account first), just a guard against a stray click locking out the only
// session currently open, with no other admin account to fix it from.
func (s *Server) handleDeleteAccount(w http.ResponseWriter, r *http.Request) {
	if !RequireRole(w, r, hyvev1alpha1.RoleAdmin, hyvev1alpha1.RoleSuperadmin) {
		return
	}
	username := r.PathValue("username")

	if caller, ok := UsernameFromContext(r.Context()); ok && caller == username {
		writeError(w, http.StatusBadRequest, "cannot delete your own account")
		return
	}

	ns := s.TenantNamespace(r)
	binding, err := s.findBindingBySubject(r.Context(), ns, orgdb.SubjectTypeLocal, username)
	if err != nil {
		writeError(w, http.StatusNotFound, "account not found")
		return
	}
	// Same rule as the list endpoint: an ordinary admin can never delete a
	// superadmin account, even one sharing its scope — reported as "not
	// found" rather than "forbidden" so it's indistinguishable from a
	// truly nonexistent account, matching the list endpoint's own "never
	// visible" stance rather than confirming a superadmin account exists.
	if callerRole, _ := RoleFromContext(r.Context()); binding.Role == hyvev1alpha1.RoleSuperadmin && callerRole != hyvev1alpha1.RoleSuperadmin {
		writeError(w, http.StatusNotFound, "account not found")
		return
	}
	if err := s.OrgStore.DeleteBinding(r.Context(), binding.ID); err != nil {
		log.Printf("api: failed to delete access binding for %q: %v", username, err)
		writeError(w, http.StatusInternalServerError, "failed to delete account")
		return
	}

	// The password hash lived on the binding row itself (password_hash,
	// Milestone 10 Part C) — deleting the binding above already removed it
	// in the same write, unlike the old design's separate paired Secret.

	w.WriteHeader(http.StatusNoContent)
}

type updateAccountPasswordRequest struct {
	// CurrentPassword is required only when the caller is changing their
	// own password (proves the session hasn't been left open on a shared
	// machine) — ignored entirely on an admin-driven reset of someone
	// else's account, which is authorized by role instead.
	CurrentPassword string `json:"currentPassword,omitempty"`
	NewPassword     string `json:"newPassword"`
}

// handleUpdateAccountPassword covers two cases behind one endpoint:
//
//   - Self-service: the caller changes their own password. Reachable by
//     any authenticated+bound role — requireRole (mounted ahead of this on
//     every /api/* route) only resolves and attaches a role, it doesn't
//     gate one, so a read-only or custom-role account can reach this same
//     as admin/superadmin. Requires CurrentPassword, verified against the
//     existing hash first.
//   - Admin-driven reset: an admin/superadmin changes someone else's
//     password, same RequireRole gate and superadmin-visibility rule as
//     handleDeleteAccount. No CurrentPassword needed.
func (s *Server) handleUpdateAccountPassword(w http.ResponseWriter, r *http.Request) {
	username := r.PathValue("username")
	ctx := r.Context()

	caller, ok := UsernameFromContext(ctx)
	if !ok {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}
	callerRole, _ := RoleFromContext(ctx)
	isSelf := caller == username

	if !isSelf {
		if !RequireRole(w, r, hyvev1alpha1.RoleAdmin, hyvev1alpha1.RoleSuperadmin) {
			return
		}
	}

	var req updateAccountPasswordRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.NewPassword == "" {
		writeError(w, http.StatusBadRequest, "newPassword is required")
		return
	}
	if isSelf && req.CurrentPassword == "" {
		writeError(w, http.StatusBadRequest, "currentPassword is required")
		return
	}

	// A superadmin binding always lives in s.Namespace regardless of "act
	// as" (see handleCreateAccount) — s.TenantNamespace(r) would otherwise
	// resolve a superadmin's own self-change to whatever tenant they
	// happen to be "Viewing", which is the wrong scope for their own
	// binding.
	ns := s.TenantNamespace(r)
	if isSelf && callerRole == hyvev1alpha1.RoleSuperadmin {
		ns = s.Namespace
	}

	binding, err := s.findBindingBySubject(ctx, ns, orgdb.SubjectTypeLocal, username)
	if err != nil {
		writeError(w, http.StatusNotFound, "account not found")
		return
	}
	// Same visibility rule as list/delete: an ordinary admin resetting
	// someone else's password can never even see a superadmin account.
	if !isSelf && binding.Role == hyvev1alpha1.RoleSuperadmin && callerRole != hyvev1alpha1.RoleSuperadmin {
		writeError(w, http.StatusNotFound, "account not found")
		return
	}

	if isSelf {
		// 400, not 401: apiFetch (web/src/lib/api/client.ts) treats any 401
		// from the server as "this session itself is invalid" and force-logs-
		// out the caller globally — exactly the wrong reaction to a merely
		// mistyped current password in an otherwise-valid session.
		if binding.PasswordHash == nil || !VerifyPassword(*binding.PasswordHash, req.CurrentPassword) {
			writeError(w, http.StatusBadRequest, "current password is incorrect")
			return
		}
	}

	hash, err := HashPassword(req.NewPassword)
	if err != nil {
		log.Printf("api: failed to hash new password for %q: %v", username, err)
		writeError(w, http.StatusInternalServerError, "failed to update password")
		return
	}
	if err := s.OrgStore.SetBindingPassword(ctx, binding.ID, hash); err != nil {
		log.Printf("api: failed to update password for %q: %v", username, err)
		writeError(w, http.StatusInternalServerError, "failed to update password")
		return
	}

	// Fires for both branches this handler covers — self-service and
	// admin-driven — unlike Pangolin's own NotifyResetPassword, which
	// only has the one (forgot-password) reset path to notify from (see
	// HYVE-EMAIL-IMPLEMENTATION-PLAN.md's Milestone 2 template table).
	if binding.Email != nil {
		sendAccountNotification(s, r, *binding.Email, email.TemplatePasswordChanged, email.PasswordChangedData{Username: binding.Identity})
	}

	w.WriteHeader(http.StatusNoContent)
}
