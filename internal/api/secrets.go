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
	mux.HandleFunc("GET /secrets", s.handleListSecrets)
	mux.HandleFunc("GET /secrets/{key}", s.handleGetSecret)
	mux.HandleFunc("PUT /secrets/{key}", s.handleSetSecret)
	mux.HandleFunc("DELETE /secrets/{key}", s.handleUnsetSecret)
}

// getCliSecret fetches the shared secret, treating NotFound as an empty
// object rather than an error — it's created lazily on first handleSetSecret
// call, mirroring internal/repository's own "configured but doesn't exist
// yet" stance for the local env store.
func (s *Server) getCliSecret(r *http.Request) (*corev1.Secret, error) {
	tenantNS := s.TenantNamespace(r)
	var secret corev1.Secret
	err := s.Client.Get(r.Context(), types.NamespacedName{Namespace: tenantNS, Name: cliSecretsName}, &secret)
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

	ctx := r.Context()
	tenantNS := s.TenantNamespace(r)
	var secret corev1.Secret
	err := s.Client.Get(ctx, types.NamespacedName{Namespace: tenantNS, Name: cliSecretsName}, &secret)
	if apierrors.IsNotFound(err) {
		secret = corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: cliSecretsName, Namespace: tenantNS},
			Data:       map[string][]byte{key: []byte(req.Value)},
		}
		if err := s.Client.Create(ctx, &secret); err != nil {
			log.Printf("api: failed to create cli secrets: %v", err)
			writeError(w, http.StatusInternalServerError, "failed to set secret")
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err != nil {
		log.Printf("api: failed to get cli secrets: %v", err)
		writeError(w, http.StatusInternalServerError, "failed to set secret")
		return
	}

	if secret.Data == nil {
		secret.Data = map[string][]byte{}
	}
	secret.Data[key] = []byte(req.Value)
	if err := s.Client.Update(ctx, &secret); err != nil {
		log.Printf("api: failed to update cli secrets: %v", err)
		writeError(w, http.StatusInternalServerError, "failed to set secret")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleUnsetSecret(w http.ResponseWriter, r *http.Request) {
	if !RequireRole(w, r, hyvev1alpha1.RoleAdmin) {
		return
	}
	key := r.PathValue("key")

	ctx := r.Context()
	var secret corev1.Secret
	err := s.Client.Get(ctx, types.NamespacedName{Namespace: s.TenantNamespace(r), Name: cliSecretsName}, &secret)
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
	if err := s.Client.Update(ctx, &secret); err != nil {
		log.Printf("api: failed to update cli secrets: %v", err)
		writeError(w, http.StatusInternalServerError, "failed to unset secret")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
