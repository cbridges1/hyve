package api

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"sort"

	hyvev1alpha1 "github.com/cbridges1/hyve/internal/apis/hyve/v1alpha1"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

// cliSecretsName is the single Kubernetes Secret every cluster-mode `hyve
// env secrets` command reads/writes — one object per *tenant namespace*
// (see Server.TenantNamespace), not a single object shared across every
// tenant on a Phase 2 shared install. Under Phase 1 (one install per
// tenant), "one shared object per hyve-api deployment" and "one per
// tenant" were the same thing; they aren't anymore, so this now resolves
// per-request like every other tenant-scoped object.
const cliSecretsName = "hyve-cli-secrets"

// secretKeyPattern matches valid environment variable names — mirrors
// internal/repository's own secretKeyPattern (duplicated, not imported:
// internal/api never depends on internal/repository, a CLI-local-only
// concept).
var secretKeyPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func (s *Server) registerSecretsRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /secrets", s.requireOrganizationNotMigrating(s.handleListSecrets))
	mux.HandleFunc("GET /secrets/{key}", s.requireOrganizationNotMigrating(s.handleGetSecret))
	mux.HandleFunc("PATCH /secrets", s.requireOrganizationNotMigrating(s.handleSetSecrets))
	mux.HandleFunc("PUT /secrets/{key}", s.requireOrganizationNotMigrating(s.handleSetSecret))
	mux.HandleFunc("DELETE /secrets/{key}", s.requireOrganizationNotMigrating(s.handleUnsetSecret))
}

// getCliSecret fetches the shared secret, treating NotFound as an empty
// object rather than an error — it's created lazily on first handleSetSecret
// call, mirroring internal/repository's own "configured but doesn't exist
// yet" stance for the local env store. Resolves through resourceClient
// (Milestone 10 Part C), not s.Client directly — an organization on a
// remote reconciling cluster has its hyve-cli-secrets Secret there too, not
// on this control plane's own home cluster.
func (s *Server) getCliSecret(r *http.Request) (*corev1.Secret, error) {
	tenantNS := s.TenantNamespace(r)
	c, err := s.resourceClient(r.Context(), tenantNS)
	if err != nil {
		return nil, err
	}
	var secret corev1.Secret
	err = c.Get(r.Context(), types.NamespacedName{Namespace: tenantNS, Name: cliSecretsName}, &secret)
	if apierrors.IsNotFound(err) {
		return &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: cliSecretsName, Namespace: tenantNS},
			Data:       map[string][]byte{},
		}, nil
	}
	if err != nil {
		return nil, err
	}
	return &secret, nil
}

// handleListSecrets returns key names only by default — readable by any
// authenticated role, since names alone aren't sensitive. ?values=true
// additionally requires RoleAdmin and returns the full key->value map;
// LoadEnvironmentSecrets (cmd/shared) uses this form in one round trip
// rather than fetching every key individually.
func (s *Server) handleListSecrets(w http.ResponseWriter, r *http.Request) {
	withValues := r.URL.Query().Get("values") == "true"
	if withValues && !RequireRole(w, r, hyvev1alpha1.RoleAdmin) {
		return
	}

	secret, err := s.getCliSecret(r)
	if err != nil {
		log.Printf("api: failed to get cli secrets: %v", err)
		writeError(w, http.StatusInternalServerError, "failed to list secrets")
		return
	}

	if withValues {
		out := make(map[string]string, len(secret.Data))
		for k, v := range secret.Data {
			out[k] = string(v)
		}
		writeJSON(w, http.StatusOK, out)
		return
	}

	keys := make([]string, 0, len(secret.Data))
	for k := range secret.Data {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	writeJSON(w, http.StatusOK, keys)
}

func (s *Server) handleGetSecret(w http.ResponseWriter, r *http.Request) {
	if !RequireRole(w, r, hyvev1alpha1.RoleAdmin) {
		return
	}
	key := r.PathValue("key")

	secret, err := s.getCliSecret(r)
	if err != nil {
		log.Printf("api: failed to get cli secrets: %v", err)
		writeError(w, http.StatusInternalServerError, "failed to get secret")
		return
	}
	value, ok := secret.Data[key]
	if !ok {
		writeError(w, http.StatusNotFound, "secret not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"key": key, "value": string(value)})
}

type setSecretRequest struct {
	Value string `json:"value"`
}

func (s *Server) handleSetSecret(w http.ResponseWriter, r *http.Request) {
	if !RequireRole(w, r, hyvev1alpha1.RoleAdmin) {
		return
	}
	key := r.PathValue("key")
	if !secretKeyPattern.MatchString(key) {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid key %q: must match %s", key, secretKeyPattern.String()))
		return
	}

	var req setSecretRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := s.mergeCliSecrets(r, map[string]string{key: req.Value}); err != nil {
		log.Printf("api: failed to set cli secret: %v", err)
		writeError(w, http.StatusInternalServerError, "failed to set secret")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type setSecretsRequest struct {
	Values map[string]string `json:"values"`
}

// handleSetSecrets sets several keys in one write — what the web console's
// .env import uses, so an import either lands whole or not at all, instead
// of N separate PUTs racing each other's read-modify-write of the same
// Secret. Keys not named are left as they are. Every key is validated
// before anything is written.
func (s *Server) handleSetSecrets(w http.ResponseWriter, r *http.Request) {
	if !RequireRole(w, r, hyvev1alpha1.RoleAdmin) {
		return
	}
	var req setSecretsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if len(req.Values) == 0 {
		writeError(w, http.StatusBadRequest, "values must name at least one key")
		return
	}
	for key := range req.Values {
		if !secretKeyPattern.MatchString(key) {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid key %q: must match %s", key, secretKeyPattern.String()))
			return
		}
	}
	if err := s.mergeCliSecrets(r, req.Values); err != nil {
		log.Printf("api: failed to set cli secrets: %v", err)
		writeError(w, http.StatusInternalServerError, "failed to set secrets")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// mergeCliSecrets writes values into the tenant's hyve-cli-secrets Secret
// in a single create or update, leaving other keys untouched.
func (s *Server) mergeCliSecrets(r *http.Request, values map[string]string) error {
	ctx := r.Context()
	tenantNS := s.TenantNamespace(r)
	c, err := s.resourceClient(ctx, tenantNS)
	if err != nil {
		return fmt.Errorf("resolve resource client: %w", err)
	}
	var secret corev1.Secret
	err = c.Get(ctx, types.NamespacedName{Namespace: tenantNS, Name: cliSecretsName}, &secret)
	if apierrors.IsNotFound(err) {
		secret = corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: cliSecretsName, Namespace: tenantNS},
			Data:       make(map[string][]byte, len(values)),
		}
		for k, v := range values {
			secret.Data[k] = []byte(v)
		}
		return c.Create(ctx, &secret)
	}
	if err != nil {
		return fmt.Errorf("get %s: %w", cliSecretsName, err)
	}
	if secret.Data == nil {
		secret.Data = make(map[string][]byte, len(values))
	}
	for k, v := range values {
		secret.Data[k] = []byte(v)
	}
	return c.Update(ctx, &secret)
}

func (s *Server) handleUnsetSecret(w http.ResponseWriter, r *http.Request) {
	if !RequireRole(w, r, hyvev1alpha1.RoleAdmin) {
		return
	}
	key := r.PathValue("key")

	ctx := r.Context()
	tenantNS := s.TenantNamespace(r)
	c, err := s.resourceClient(ctx, tenantNS)
	if err != nil {
		log.Printf("api: failed to resolve resource client for cli secrets: %v", err)
		writeError(w, http.StatusInternalServerError, "failed to unset secret")
		return
	}
	var secret corev1.Secret
	err = c.Get(ctx, types.NamespacedName{Namespace: tenantNS, Name: cliSecretsName}, &secret)
	if apierrors.IsNotFound(err) {
		w.WriteHeader(http.StatusNoContent) // nothing to unset — idempotent, matches local UnsetSecret
		return
	}
	if err != nil {
		log.Printf("api: failed to get cli secrets: %v", err)
		writeError(w, http.StatusInternalServerError, "failed to unset secret")
		return
	}

	delete(secret.Data, key)
	if err := c.Update(ctx, &secret); err != nil {
		log.Printf("api: failed to update cli secrets: %v", err)
		writeError(w, http.StatusInternalServerError, "failed to unset secret")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
