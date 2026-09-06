package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"

	hyvev1alpha1 "github.com/cbridges1/hyve/internal/apis/hyve/v1alpha1"

	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// registerEnvironmentRoutes wires POST/GET /environments — mounted under
// /api/ (behind requireAuth+requireRole) by Server.Routes. See
// HYVE-MULTI-TENANCY-PLAN.md's "New endpoint: POST /environments" section
// (GET wasn't in the original spec but is the obvious admin-UX read half of
// it — same superadmin gate, no separate design question).
func (s *Server) registerEnvironmentRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /environments", s.handleCreateEnvironment)
	mux.HandleFunc("GET /environments", s.handleListEnvironments)
}

// handleListEnvironments lists every HyveEnvironment — the same list a
// `--namespace hyve-system` migration enumerates, exposed for an admin UI.
// Superadmin-only, same reasoning as create: this is a cross-namespace view
// by definition.
func (s *Server) handleListEnvironments(w http.ResponseWriter, r *http.Request) {
	if !RequireRole(w, r, hyvev1alpha1.RoleSuperadmin) {
		return
	}
	var list hyvev1alpha1.HyveEnvironmentList
	if err := s.Client.List(r.Context(), &list, client.InNamespace(s.Namespace)); err != nil {
		log.Printf("api: failed to list environments: %v", err)
		writeError(w, http.StatusInternalServerError, "failed to list environments")
		return
	}
	out := make([]environmentDTO, 0, len(list.Items))
	for _, env := range list.Items {
		out = append(out, environmentDTO{Name: env.Name, Namespace: env.Spec.Namespace})
	}
	writeJSON(w, http.StatusOK, out)
}

type createEnvironmentRequest struct {
	Name string `json:"name"`
}

type environmentDTO struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
}

// handleCreateEnvironment turns a namespace into a real, hyve-recognized
// tenant — nothing more. Creating the first user in it is a separate,
// already-existing call (POST /accounts, with an explicit namespace — see
// createAccountRequest.Namespace). Gated to superadmin only: this is the
// one operation that spans namespaces by design, and the one place
// HYVE-MULTI-TENANCY-PLAN.md's new cluster-scoped role actually exists for.
//
// Each of the three steps checks "does this already exist" before
// creating, so a re-POST after a partial failure (e.g. the namespace got
// created but the RBAC scaffolding failed) fills in what's missing instead
// of erroring on "already exists" — no rollback needed, matching
// internal/migrate's own objectExists precedent.
func (s *Server) handleCreateEnvironment(w http.ResponseWriter, r *http.Request) {
	if !RequireRole(w, r, hyvev1alpha1.RoleSuperadmin) {
		return
	}
	var req createEnvironmentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	ctx := r.Context()
	name := req.Name

	if err := s.ensureNamespace(ctx, name); err != nil {
		log.Printf("api: failed to ensure namespace %q: %v", name, err)
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("failed to create environment: %v", err))
		return
	}
	if err := s.ensureAccessRoleScaffolding(ctx, name); err != nil {
		log.Printf("api: failed to ensure RBAC scaffolding for %q: %v", name, err)
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("failed to create environment: %v", err))
		return
	}
	if err := s.ensureHyveEnvironment(ctx, name); err != nil {
		log.Printf("api: failed to ensure HyveEnvironment %q: %v", name, err)
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("failed to create environment: %v", err))
		return
	}

	writeJSON(w, http.StatusCreated, environmentDTO{Name: name, Namespace: name})
}

func (s *Server) ensureNamespace(ctx context.Context, name string) error {
	var ns corev1.Namespace
	err := s.Client.Get(ctx, types.NamespacedName{Name: name}, &ns)
	if err == nil {
		return nil
	}
	if !apierrors.IsNotFound(err) {
		return fmt.Errorf("check existing namespace: %w", err)
	}
	ns = corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name}}
	if err := s.Client.Create(ctx, &ns); err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("create namespace: %w", err)
	}
	return nil
}

// ensureAccessRoleScaffolding creates namespace's own
// hyve-access-admin/hyve-access-readonly ServiceAccounts + namespaced
// RoleBindings to the built-in admin/view ClusterRoles — the same two
// ServiceAccount+RoleBinding pairs deploy/helm/hyve/templates/
// api-access-roles.yaml already defines per-install under Phase 1 (one
// install = one Helm release = one namespace), created here
// programmatically instead, once per tenant, since Phase 2 has exactly one
// install total.
func (s *Server) ensureAccessRoleScaffolding(ctx context.Context, namespace string) error {
	roles := []struct {
		saName      string
		clusterRole string
	}{
		{"hyve-access-admin", "cluster-admin"},
		{"hyve-access-readonly", "view"},
	}
	for _, role := range roles {
		sa := corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Name: role.saName, Namespace: namespace}}
		if err := s.Client.Create(ctx, &sa); err != nil && !apierrors.IsAlreadyExists(err) {
			return fmt.Errorf("create ServiceAccount %s/%s: %w", namespace, role.saName, err)
		}

		rb := rbacv1.RoleBinding{
			ObjectMeta: metav1.ObjectMeta{Name: role.saName, Namespace: namespace},
			Subjects: []rbacv1.Subject{
				{Kind: "ServiceAccount", Name: role.saName, Namespace: namespace},
			},
			RoleRef: rbacv1.RoleRef{Kind: "ClusterRole", Name: role.clusterRole, APIGroup: "rbac.authorization.k8s.io"},
		}
		if err := s.Client.Create(ctx, &rb); err != nil && !apierrors.IsAlreadyExists(err) {
			return fmt.Errorf("create RoleBinding %s/%s: %w", namespace, role.saName, err)
		}
	}
	return nil
}

func (s *Server) ensureHyveEnvironment(ctx context.Context, name string) error {
	var existing hyvev1alpha1.HyveEnvironment
	err := s.Client.Get(ctx, types.NamespacedName{Namespace: s.Namespace, Name: name}, &existing)
	if err == nil {
		return nil
	}
	if !apierrors.IsNotFound(err) {
		return fmt.Errorf("check existing HyveEnvironment: %w", err)
	}
	env := &hyvev1alpha1.HyveEnvironment{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: s.Namespace},
		Spec:       hyvev1alpha1.HyveEnvironmentSpec{Namespace: name},
	}
	if err := s.Client.Create(ctx, env); err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("create HyveEnvironment: %w", err)
	}
	return nil
}
