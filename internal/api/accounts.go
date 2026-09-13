package api

import (
	"encoding/json"
	"log"
	"net/http"

	hyvev1alpha1 "github.com/cbridges1/hyve/internal/apis/hyve/v1alpha1"
	"github.com/cbridges1/hyve/internal/orgdb"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// accountDTO is the response shape for GET /api/accounts — deliberately
// carries no password hash (this never even reads the paired credentials
// Secret, only the binding). Local accounts only: an OIDC-subject binding
// has nothing here to "manage" — no password to reset, no Secret to
// delete — so it's filtered out rather than shown half-functional.
type accountDTO struct {
	Username string `json:"username"`
	Role     string `json:"role"`
}

func toAccountDTO(b orgdb.Binding) accountDTO {
	return accountDTO{Username: b.Identity, Role: b.Role}
}

// registerAccountRoutes wires /accounts — mounted under /api/ (behind
// requireAuth+requireRole) by Server.Routes. Every handler additionally
// gates RoleAdmin: account management is inherently an admin-only concern,
// same precedent as clusters/templates/workflows/resources' own
// create/delete.
func (s *Server) registerAccountRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /accounts", s.handleListAccounts)
	mux.HandleFunc("POST /accounts", s.handleCreateAccount)
	mux.HandleFunc("DELETE /accounts/{username}", s.handleDeleteAccount)
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
		// /organizations (see organizationIDForNamespace's own doc
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

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: UserCredentialsSecretName(req.Username), Namespace: secretNamespace},
		StringData: map[string]string{passwordHashDataKey: hash},
	}
	if err := s.Client.Create(ctx, secret); err != nil {
		log.Printf("api: failed to create credentials secret for %q: %v", req.Username, err)
		writeError(w, http.StatusInternalServerError, "failed to create account")
		return
	}

	binding, err := s.OrgStore.CreateBinding(ctx, orgdb.Binding{
		Namespace:               secretNamespace,
		OrganizationID:          organizationID,
		EnvironmentID:           environmentID,
		SubjectType:             orgdb.SubjectTypeLocal,
		Identity:                req.Username,
		Role:                    req.Role,
		ServiceAccountName:      serviceAccount,
		ServiceAccountNamespace: secretNamespace,
	})
	if err != nil {
		// Best-effort rollback of the Secret we just created — an orphaned
		// credentials Secret with no binding is inert (nothing looks it up
		// without a binding to name it), but cleaning up on a clear failure
		// is still better than leaving it.
		_ = s.Client.Delete(ctx, secret)
		log.Printf("api: failed to create access binding for %q: %v", req.Username, err)
		writeError(w, http.StatusInternalServerError, "failed to create account")
		return
	}

	writeJSON(w, http.StatusCreated, toAccountDTO(binding))
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

	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: UserCredentialsSecretName(binding.Identity), Namespace: ns}}
	if err := s.Client.Delete(r.Context(), secret); err != nil && !apierrors.IsNotFound(err) {
		// The binding (the actual access grant) is already gone, which is
		// what matters for security — an orphaned credentials Secret left
		// behind is inert leftover state, not worth failing the request
		// over.
		log.Printf("api: warning: failed to delete credentials secret for %q: %v", username, err)
	}

	w.WriteHeader(http.StatusNoContent)
}
