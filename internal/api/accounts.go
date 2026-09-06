package api

import (
	"encoding/json"
	"log"
	"net/http"

	hyvev1alpha1 "github.com/cbridges1/hyve/internal/apis/hyve/v1alpha1"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
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

func toAccountDTO(b *hyvev1alpha1.HyveAccessBinding) accountDTO {
	return accountDTO{Username: b.Spec.Subject.Value, Role: b.Spec.Role}
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
	var list hyvev1alpha1.HyveAccessBindingList
	if err := s.Client.List(r.Context(), &list, client.InNamespace(s.TenantNamespace(r))); err != nil {
		log.Printf("api: failed to list access bindings: %v", err)
		writeError(w, http.StatusInternalServerError, "failed to list accounts")
		return
	}
	dtos := make([]accountDTO, 0, len(list.Items))
	for i := range list.Items {
		if list.Items[i].Spec.Subject.Type != hyvev1alpha1.SubjectTypeLocal {
			continue
		}
		// A superadmin binding is never visible to an ordinary admin, even
		// one sharing its namespace (e.g. hyve-system, which today holds
		// both) — confirmed live: without this, any tenant admin could see
		// AND delete a superadmin account purely by co-locating in the same
		// namespace. Superadmin privilege is cluster-wide; only another
		// superadmin gets to see or manage it.
		if list.Items[i].Spec.Role == hyvev1alpha1.RoleSuperadmin && callerRole != hyvev1alpha1.RoleSuperadmin {
			continue
		}
		dtos = append(dtos, toAccountDTO(&list.Items[i]))
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
	// /environments. Ignored for a non-superadmin caller, who is always
	// confined to their own session's namespace regardless of what this
	// field says, same as before this field existed.
	Namespace string `json:"namespace,omitempty"`
}

// handleCreateAccount supports admin/read-only (both fixed, well-known
// ServiceAccountRefs — hyve-access-admin/hyve-access-readonly, see
// deploy/helm/hyve's api-access-roles.yaml, or POST /environments' own
// programmatic equivalent for a Phase-2 tenant) and, for a superadmin
// caller only, superadmin itself — a role can already create more of its
// own role everywhere else in this system (an admin already creates more
// admins), so a superadmin creating another superadmin isn't a new kind of
// escalation, just the same principle at the top tier. A `custom` role
// needs an operator-defined ServiceAccountRef this endpoint has no field
// for, and still goes through `hyve cluster-config api create-user --role
// custom --service-account <name>` + kubectl, unchanged.
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

	// A new superadmin's binding must land in the control-plane namespace
	// no matter what — that's the one place login ever looks for it (a
	// superadmin has no tenant of their own), and the one place the list/
	// delete handlers above already expect to find it. This overrides
	// both req.Namespace and whatever the caller's own "act as" selection
	// currently is — confirmed live this matters: creating a superadmin
	// while "Viewing" some tenant would otherwise silently create an
	// account that can never log in.
	var ns string
	if req.Role == hyvev1alpha1.RoleSuperadmin {
		ns = s.Namespace
	} else {
		ns = s.TenantNamespace(r)
		if callerRole == hyvev1alpha1.RoleSuperadmin && req.Namespace != "" {
			ns = req.Namespace
		}
	}

	var serviceAccount string
	switch req.Role {
	case hyvev1alpha1.RoleAdmin, hyvev1alpha1.RoleSuperadmin:
		// Same reasoning as cmd/api/create_user.go's own superadmin case:
		// this ServiceAccountRef only matters if the account also fetches
		// an ordinary GET /api/kubeconfig for some tenant's cluster —
		// superadmin's real privilege (host-cluster access, cross-tenant
		// account/environment management) is gated by HyveAccessBindingSpec.
		// Role alone, not this field.
		serviceAccount = "hyve-access-admin"
	case hyvev1alpha1.RoleReadOnly:
		serviceAccount = "hyve-access-readonly"
	default:
		writeError(w, http.StatusBadRequest, `role must be "admin", "read-only", or "superadmin" (a custom role needs 'hyve cluster-config api create-user --role custom' instead)`)
		return
	}

	if _, err := FindBindingBySubject(r.Context(), s.Client, ns, hyvev1alpha1.SubjectTypeLocal, req.Username); err == nil {
		writeError(w, http.StatusConflict, "an account with this username already exists")
		return
	}

	hash, err := HashPassword(req.Password)
	if err != nil {
		log.Printf("api: failed to hash password for new account %q: %v", req.Username, err)
		writeError(w, http.StatusInternalServerError, "failed to create account")
		return
	}

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: UserCredentialsSecretName(req.Username), Namespace: ns},
		StringData: map[string]string{passwordHashDataKey: hash},
	}
	if err := s.Client.Create(r.Context(), secret); err != nil {
		log.Printf("api: failed to create credentials secret for %q: %v", req.Username, err)
		writeError(w, http.StatusInternalServerError, "failed to create account")
		return
	}

	binding := &hyvev1alpha1.HyveAccessBinding{
		ObjectMeta: metav1.ObjectMeta{Name: req.Username, Namespace: ns},
		Spec: hyvev1alpha1.HyveAccessBindingSpec{
			Subject:           hyvev1alpha1.HyveAccessBindingSubject{Type: hyvev1alpha1.SubjectTypeLocal, Value: req.Username},
			Role:              req.Role,
			ServiceAccountRef: hyvev1alpha1.ServiceAccountRef{Name: serviceAccount, Namespace: ns},
		},
	}
	if err := s.Client.Create(r.Context(), binding); err != nil {
		// Best-effort rollback of the Secret we just created — an orphaned
		// credentials Secret with no binding is inert (nothing looks it up
		// without a binding to name it), but cleaning up on a clear failure
		// is still better than leaving it.
		_ = s.Client.Delete(r.Context(), secret)
		if apierrors.IsAlreadyExists(err) {
			writeError(w, http.StatusConflict, "an account with this username already exists")
			return
		}
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

	binding, err := FindBindingBySubject(r.Context(), s.Client, s.TenantNamespace(r), hyvev1alpha1.SubjectTypeLocal, username)
	if err != nil {
		writeError(w, http.StatusNotFound, "account not found")
		return
	}
	// Same rule as the list endpoint: an ordinary admin can never delete a
	// superadmin account, even one sharing its namespace — reported as
	// "not found" rather than "forbidden" so it's indistinguishable from a
	// truly nonexistent account, matching the list endpoint's own "never
	// visible" stance rather than confirming a superadmin account exists.
	if callerRole, _ := RoleFromContext(r.Context()); binding.Spec.Role == hyvev1alpha1.RoleSuperadmin && callerRole != hyvev1alpha1.RoleSuperadmin {
		writeError(w, http.StatusNotFound, "account not found")
		return
	}
	if err := s.Client.Delete(r.Context(), binding); err != nil && !apierrors.IsNotFound(err) {
		log.Printf("api: failed to delete access binding for %q: %v", username, err)
		writeError(w, http.StatusInternalServerError, "failed to delete account")
		return
	}

	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: UserCredentialsSecretName(binding.Name), Namespace: binding.Namespace}}
	if err := s.Client.Delete(r.Context(), secret); err != nil && !apierrors.IsNotFound(err) {
		// The binding (the actual access grant) is already gone, which is
		// what matters for security — an orphaned credentials Secret left
		// behind is inert leftover state, not worth failing the request
		// over.
		log.Printf("api: warning: failed to delete credentials secret for %q: %v", username, err)
	}

	w.WriteHeader(http.StatusNoContent)
}
