package api

import (
	"encoding/json"
	"log"
	"net/http"

	hyvev1alpha1 "github.com/cbridges1/hyve/internal/apis/hyve/v1alpha1"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

// defaultConfigName mirrors cmd/controller/run.go's own --config-name
// default — see Server.ConfigName's own doc comment for why both flags
// (and the chart's single .Values.controller.configName) stay in sync.
const defaultConfigName = "hyve-config"

// configName returns s.ConfigName, or defaultConfigName if unset (e.g. a
// test Server built without it).
func (s *Server) configName() string {
	if s.ConfigName != "" {
		return s.ConfigName
	}
	return defaultConfigName
}

// registerConfigRoutes wires GET/PATCH /config — mounted under /api/
// (behind requireAuth+requireRole) by Server.Routes. Superadmin-only, same
// reasoning as /environments: HyveConfig is control-plane-wide (one
// singleton, in Server.Namespace, never a tenant namespace — see
// hyvev1alpha1.HyveConfig's own doc comment), not a per-tenant resource.
func (s *Server) registerConfigRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /config", s.handleGetConfig)
	mux.HandleFunc("PATCH /config", s.handleUpdateConfig)
}

// hyveConfigDTO mirrors hyvev1alpha1.HyveConfigSpec field-for-field —
// see that type's own doc comments for what each field does. Exists
// distinguishes "no HyveConfig object at all yet" (the common case: the
// chart's own controller.hyveConfig.create defaults to false, so most
// installs start with none) from "one exists with every field at its zero
// value" — the console needs this to decide whether saving should create
// or update, and to word its own empty state correctly.
type hyveConfigDTO struct {
	Exists               bool              `json:"exists"`
	StrictResourceDelete bool              `json:"strictResourceDelete"`
	DefaultWorkflowImage string            `json:"defaultWorkflowImage,omitempty"`
	DefaultModuleImage   string            `json:"defaultModuleImage,omitempty"`
	DefaultAgentImage    string            `json:"defaultAgentImage,omitempty"`
	ImageInstalls        []imageInstallDTO `json:"imageInstalls,omitempty"`
	ImagePullSecrets     []string          `json:"imagePullSecrets,omitempty"`
}

type imageInstallDTO struct {
	Image   string `json:"image"`
	Install string `json:"install"`
}

func dtoFromHyveConfig(cfg *hyvev1alpha1.HyveConfig) hyveConfigDTO {
	installs := make([]imageInstallDTO, 0, len(cfg.Spec.ImageInstalls))
	for _, ii := range cfg.Spec.ImageInstalls {
		installs = append(installs, imageInstallDTO{Image: ii.Image, Install: ii.Install})
	}
	return hyveConfigDTO{
		Exists:               true,
		StrictResourceDelete: cfg.Spec.StrictResourceDelete,
		DefaultWorkflowImage: cfg.Spec.DefaultWorkflowImage,
		DefaultModuleImage:   cfg.Spec.DefaultModuleImage,
		DefaultAgentImage:    cfg.Spec.DefaultAgentImage,
		ImageInstalls:        installs,
		ImagePullSecrets:     cfg.Spec.ImagePullSecrets,
	}
}

func specFromDTO(dto hyveConfigDTO) hyvev1alpha1.HyveConfigSpec {
	installs := make([]hyvev1alpha1.ImageInstall, 0, len(dto.ImageInstalls))
	for _, ii := range dto.ImageInstalls {
		installs = append(installs, hyvev1alpha1.ImageInstall{Image: ii.Image, Install: ii.Install})
	}
	return hyvev1alpha1.HyveConfigSpec{
		StrictResourceDelete: dto.StrictResourceDelete,
		DefaultWorkflowImage: dto.DefaultWorkflowImage,
		DefaultModuleImage:   dto.DefaultModuleImage,
		DefaultAgentImage:    dto.DefaultAgentImage,
		ImageInstalls:        installs,
		ImagePullSecrets:     dto.ImagePullSecrets,
	}
}

// handleGetConfig returns {exists: false} rather than 404 when no
// HyveConfig object exists yet — that's the common, expected state for a
// fresh install (see hyveConfigDTO's own doc comment), not an error
// condition a caller needs to handle specially.
func (s *Server) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	if !RequireRole(w, r, hyvev1alpha1.RoleSuperadmin) {
		return
	}
	var cfg hyvev1alpha1.HyveConfig
	err := s.Client.Get(r.Context(), types.NamespacedName{Namespace: s.Namespace, Name: s.configName()}, &cfg)
	if err != nil {
		if apierrors.IsNotFound(err) {
			writeJSON(w, http.StatusOK, hyveConfigDTO{Exists: false})
			return
		}
		log.Printf("api: failed to get HyveConfig %s/%s: %v", s.Namespace, s.configName(), err)
		writeError(w, http.StatusInternalServerError, "failed to get config")
		return
	}
	writeJSON(w, http.StatusOK, dtoFromHyveConfig(&cfg))
}

// handleUpdateConfig creates the singleton HyveConfig if it doesn't exist
// yet, or updates it in place if it does — a caller submitting a form with
// every field at once shouldn't have to know or care which case applies,
// mirroring create-user's/self-register-host's own upsert precedent
// elsewhere in this codebase for exactly this "singleton config object"
// shape.
func (s *Server) handleUpdateConfig(w http.ResponseWriter, r *http.Request) {
	if !RequireRole(w, r, hyvev1alpha1.RoleSuperadmin) {
		return
	}
	var req hyveConfigDTO
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	ctx := r.Context()
	key := types.NamespacedName{Namespace: s.Namespace, Name: s.configName()}

	var existing hyvev1alpha1.HyveConfig
	err := s.Client.Get(ctx, key, &existing)
	if apierrors.IsNotFound(err) {
		cfg := &hyvev1alpha1.HyveConfig{
			ObjectMeta: metav1.ObjectMeta{Name: s.configName(), Namespace: s.Namespace},
			Spec:       specFromDTO(req),
		}
		if err := s.Client.Create(ctx, cfg); err != nil {
			log.Printf("api: failed to create HyveConfig %s/%s: %v", s.Namespace, s.configName(), err)
			writeError(w, http.StatusInternalServerError, "failed to create config")
			return
		}
		writeJSON(w, http.StatusOK, dtoFromHyveConfig(cfg))
		return
	}
	if err != nil {
		log.Printf("api: failed to get HyveConfig %s/%s: %v", s.Namespace, s.configName(), err)
		writeError(w, http.StatusInternalServerError, "failed to get config")
		return
	}

	existing.Spec = specFromDTO(req)
	if err := s.Client.Update(ctx, &existing); err != nil {
		log.Printf("api: failed to update HyveConfig %s/%s: %v", s.Namespace, s.configName(), err)
		writeError(w, http.StatusInternalServerError, "failed to update config")
		return
	}
	writeJSON(w, http.StatusOK, dtoFromHyveConfig(&existing))
}
