package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	hyvev1alpha1 "github.com/cbridges1/hyve/internal/apis/hyve/v1alpha1"
	"github.com/cbridges1/hyve/internal/orgdb"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// requireOrganizationNotMigrating wraps next so any request whose
// TenantNamespace resolves to a currently-migrating organization
// (reconciling_cluster_migration_status = 'migrating') gets 423 Locked
// instead of reaching next — the proposal's decided lock-don't-dual-serve
// design (see handlePatchOrganization). Applied at registration time to
// every route in clusters.go/templates.go/workflows.go/workflowruns.go/
// resources.go — the five files whose handlers resolve through
// resourceClient/resourceClientset — never globally, since organization/
// account/config management itself (Store rows, not cluster resources)
// isn't part of what a reconciling-cluster migration actually moves.
//
// A resolution error here is treated as "not locked" (falls through to
// next) rather than surfaced as its own 500 — the underlying handler's own
// resourceClient call will hit the identical error and report it properly;
// this check's only job is the 423 case, not a second error-reporting
// path for unrelated failures.
func (s *Server) requireOrganizationNotMigrating(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.OrgStore != nil {
			namespace := s.TenantNamespace(r)
			if namespace != s.Namespace {
				if org, err := s.OrgStore.GetOrganizationByName(r.Context(), namespace); err == nil && org.ReconcilingClusterMigrationStatus != nil {
					writeError(w, http.StatusLocked, fmt.Sprintf("organization %q is migrating to a new reconciling cluster — try again shortly", namespace))
					return
				}
			}
		}
		next(w, r)
	}
}

// reconcilingClusterKubeconfigSecretKey is the Secret data key a
// reconciling cluster's kubeconfig is stored under — same convention a
// plain `kubectl create secret generic --from-file=kubeconfig=...` would
// use, so an operator inspecting the Secret by hand (never by this API,
// which never echoes the content back) sees a familiar shape.
const reconcilingClusterKubeconfigSecretKey = "kubeconfig"

func (s *Server) registerReconcilingClusterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /reconciling-clusters", s.handleCreateReconcilingCluster)
	mux.HandleFunc("GET /reconciling-clusters", s.handleListReconcilingClusters)
}

// reconcilingClusterDTO deliberately excludes the kubeconfig itself —
// internal/orgdb.ReconcilingCluster only ever stores a Secret reference
// (KubeconfigSecretNamespace/Name), never the content, so there's nothing
// for this DTO to leak even by accident.
type reconcilingClusterDTO struct {
	Name          string  `json:"name"`
	Reachable     *bool   `json:"reachable,omitempty"`
	LastCheckedAt *string `json:"lastCheckedAt,omitempty"`
	LastError     *string `json:"lastError,omitempty"`
}

func toReconcilingClusterDTO(rc orgdb.ReconcilingCluster) reconcilingClusterDTO {
	dto := reconcilingClusterDTO{Name: rc.Name, Reachable: rc.Reachable, LastError: rc.LastError}
	if rc.LastCheckedAt != nil {
		s := rc.LastCheckedAt.Format(time.RFC3339)
		dto.LastCheckedAt = &s
	}
	return dto
}

type createReconcilingClusterRequest struct {
	Name       string `json:"name"`
	Kubeconfig string `json:"kubeconfig"`
}

// handleCreateReconcilingCluster registers a new physical cluster
// hyve-controller can reconcile organizations' infrastructure against
// (Milestone 6 — see HYVE-ORGANIZATION-MODEL-PROPOSAL.md's "Per-
// organization reconciling cluster" section, nexus-config/docs).
// Superadmin-only: like POST /organizations, this is one of the few
// operations that spans the whole install rather than one tenant.
//
// The kubeconfig itself is written to a Secret in this control plane's own
// home namespace (s.Namespace) and never stored in Store or echoed back in
// any response — only the Secret reference is. Idempotent by name: a
// re-POST with the same name rotates that Secret's content (a real,
// expected operational need — kubeconfigs expire/get regenerated) rather
// than erroring, matching every other "register" endpoint's own
// check-then-create-or-update precedent in this package.
func (s *Server) handleCreateReconcilingCluster(w http.ResponseWriter, r *http.Request) {
	if !RequireRole(w, r, hyvev1alpha1.RoleSuperadmin) {
		return
	}
	var req createReconcilingClusterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	if req.Kubeconfig == "" {
		writeError(w, http.StatusBadRequest, "kubeconfig is required")
		return
	}
	if _, err := clientcmd.RESTConfigFromKubeConfig([]byte(req.Kubeconfig)); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid kubeconfig: %v", err))
		return
	}

	ctx := r.Context()
	secretName := "reconciling-cluster-" + req.Name + "-kubeconfig"

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: secretName, Namespace: s.Namespace},
		Data:       map[string][]byte{reconcilingClusterKubeconfigSecretKey: []byte(req.Kubeconfig)},
	}
	if err := s.Client.Create(ctx, secret); err != nil {
		if !apierrors.IsAlreadyExists(err) {
			log.Printf("api: failed to create kubeconfig secret for reconciling cluster %q: %v", req.Name, err)
			writeError(w, http.StatusInternalServerError, "failed to store kubeconfig")
			return
		}
		if err := s.Client.Update(ctx, secret); err != nil {
			log.Printf("api: failed to rotate kubeconfig secret for reconciling cluster %q: %v", req.Name, err)
			writeError(w, http.StatusInternalServerError, "failed to rotate kubeconfig")
			return
		}
		// A rotated kubeconfig invalidates any cached client for this
		// cluster — the next resourceClient/health-check call must rebuild
		// it, not keep talking through stale (possibly now-revoked)
		// credentials.
		s.invalidateReconcilingClusterClientByName(ctx, req.Name)
	}

	rc, err := s.OrgStore.GetReconcilingClusterByName(ctx, req.Name)
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, toReconcilingClusterDTO(rc))
		return
	case err == orgdb.ErrNotFound:
		rc, err = s.OrgStore.CreateReconcilingCluster(ctx, orgdb.ReconcilingCluster{
			Name:                      req.Name,
			KubeconfigSecretNamespace: s.Namespace,
			KubeconfigSecretName:      secretName,
		})
		if err != nil {
			log.Printf("api: failed to register reconciling cluster %q: %v", req.Name, err)
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("failed to register reconciling cluster: %v", err))
			return
		}
		writeJSON(w, http.StatusCreated, toReconcilingClusterDTO(rc))
	default:
		log.Printf("api: failed to check existing reconciling cluster %q: %v", req.Name, err)
		writeError(w, http.StatusInternalServerError, "failed to register reconciling cluster")
	}
}

func (s *Server) handleListReconcilingClusters(w http.ResponseWriter, r *http.Request) {
	if !RequireRole(w, r, hyvev1alpha1.RoleSuperadmin) {
		return
	}
	list, err := s.OrgStore.ListReconcilingClusters(r.Context())
	if err != nil {
		log.Printf("api: failed to list reconciling clusters: %v", err)
		writeError(w, http.StatusInternalServerError, "failed to list reconciling clusters")
		return
	}
	out := make([]reconcilingClusterDTO, 0, len(list))
	for _, rc := range list {
		out = append(out, toReconcilingClusterDTO(rc))
	}
	writeJSON(w, http.StatusOK, out)
}

// resourceClient resolves which Kubernetes client a request touching
// namespace's ClusterDefinition/Template/Workflow/Resource (and
// WorkflowRun) objects should use — s.Client (the control plane's own home
// cluster) unless namespace belongs to an Organization that's been moved
// to a different reconciling cluster (Milestone 6). Every handler in
// clusters.go/templates.go/workflows.go/workflowruns.go/resources.go
// resolves through this instead of using s.Client directly for those five
// types — see HYVE-ORGANIZATION-MODEL-PROPOSAL.md's "Per-organization
// reconciling cluster" section for why this needs to exist at all.
//
// Mirrors organizationIDForNamespace's own graceful-fallback shape
// (identity.go): namespace == s.Namespace, a nil OrgStore, or no matching
// Organization all fall back to s.Client, exactly like every other
// Milestone-3-onward resolution helper in this package — additive, never a
// breaking change for anything that predates Milestone 6.
func (s *Server) resourceClient(ctx context.Context, namespace string) (client.Client, error) {
	handle, err := s.reconcilingClusterHandleForNamespace(ctx, namespace)
	if err != nil || handle == nil {
		return s.Client, err
	}
	return handle.Client, nil
}

// resourceClientset is resourceClient's own counterpart for the one place
// this package needs a raw client-go Interface instead of a
// controller-runtime client.Client — handleGetClusterEvents' Events()
// field-selector list (see that handler's own doc comment on why it can't
// go through the cached client either way). Without this, a migrated
// organization's cluster events would be read from the control plane's own
// home cluster instead of wherever that organization's ClusterDefinition
// actually lives and emits events — a real, silent wrong-cluster read, not
// just a missing feature.
func (s *Server) resourceClientset(ctx context.Context, namespace string) (kubernetes.Interface, error) {
	handle, err := s.reconcilingClusterHandleForNamespace(ctx, namespace)
	if err != nil || handle == nil {
		return s.Clientset, err
	}
	return handle.Clientset, nil
}

// reconcilingClusterHandle bundles the two Kubernetes client shapes this
// package needs per reconciling cluster — a cached controller-runtime
// client.Client (for the four CRD types) and a raw client-go
// kubernetes.Interface (for the Events field-selector list) — built once
// from the same kubeconfig and cached together, rather than parsing that
// kubeconfig and dialing twice.
type reconcilingClusterHandle struct {
	Client    client.Client
	Clientset kubernetes.Interface
}

// reconcilingClusterHandleForNamespace resolves namespace to its
// Organization's reconciling cluster, if any — nil (with a nil error)
// means "no override, use the control plane's own home cluster
// (s.Client/s.Clientset)", mirroring organizationIDForNamespace's own
// graceful-fallback shape (identity.go): namespace == s.Namespace, a nil
// OrgStore, no matching Organization, or an Organization with no
// reconciling cluster override all resolve to nil here — additive, never a
// breaking change for anything that predates Milestone 6.
func (s *Server) reconcilingClusterHandleForNamespace(ctx context.Context, namespace string) (*reconcilingClusterHandle, error) {
	if namespace == s.Namespace || s.OrgStore == nil {
		return nil, nil
	}
	org, err := s.OrgStore.GetOrganizationByName(ctx, namespace)
	if err == orgdb.ErrNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("resolve organization for namespace %q: %w", namespace, err)
	}
	if org.ReconcilingClusterID == nil {
		return nil, nil
	}
	handle, err := s.reconcilingClusterClientHandle(ctx, *org.ReconcilingClusterID)
	if err != nil {
		return nil, err
	}
	return handle, nil
}

// reconcilingClusterClientHandle returns a cached reconcilingClusterHandle
// for reconcilingClusterID, building and caching one on first use from
// that cluster's own registered kubeconfig Secret (read via s.Client — the
// Secret always lives on the control plane's own home cluster, regardless
// of which cluster it describes). Safe for concurrent use.
func (s *Server) reconcilingClusterClientHandle(ctx context.Context, reconcilingClusterID string) (*reconcilingClusterHandle, error) {
	s.reconcilingClientsMu.RLock()
	h, ok := s.reconcilingClusterClients[reconcilingClusterID]
	s.reconcilingClientsMu.RUnlock()
	if ok {
		return h, nil
	}

	s.reconcilingClientsMu.Lock()
	defer s.reconcilingClientsMu.Unlock()
	// Re-check under the write lock: another request may have already
	// built this same cluster's handle while this one waited for the lock.
	if h, ok := s.reconcilingClusterClients[reconcilingClusterID]; ok {
		return h, nil
	}

	rc, err := s.OrgStore.GetReconcilingCluster(ctx, reconcilingClusterID)
	if err != nil {
		return nil, fmt.Errorf("get reconciling cluster %q: %w", reconcilingClusterID, err)
	}
	h, err = s.buildReconcilingClusterHandle(ctx, rc)
	if err != nil {
		return nil, err
	}
	if s.reconcilingClusterClients == nil {
		s.reconcilingClusterClients = map[string]*reconcilingClusterHandle{}
	}
	s.reconcilingClusterClients[reconcilingClusterID] = h
	return h, nil
}

func (s *Server) buildReconcilingClusterHandle(ctx context.Context, rc orgdb.ReconcilingCluster) (*reconcilingClusterHandle, error) {
	var secret corev1.Secret
	key := types.NamespacedName{Namespace: rc.KubeconfigSecretNamespace, Name: rc.KubeconfigSecretName}
	if err := s.Client.Get(ctx, key, &secret); err != nil {
		return nil, fmt.Errorf("get kubeconfig secret for reconciling cluster %q: %w", rc.Name, err)
	}
	kubeconfigBytes, ok := secret.Data[reconcilingClusterKubeconfigSecretKey]
	if !ok {
		return nil, fmt.Errorf("kubeconfig secret %s/%s for reconciling cluster %q has no %q key", key.Namespace, key.Name, rc.Name, reconcilingClusterKubeconfigSecretKey)
	}
	restCfg, err := clientcmd.RESTConfigFromKubeConfig(kubeconfigBytes)
	if err != nil {
		return nil, fmt.Errorf("parse kubeconfig for reconciling cluster %q: %w", rc.Name, err)
	}
	c, err := client.New(restCfg, client.Options{Scheme: s.Client.Scheme()})
	if err != nil {
		return nil, fmt.Errorf("build client for reconciling cluster %q: %w", rc.Name, err)
	}
	cs, err := kubernetes.NewForConfig(restCfg)
	if err != nil {
		return nil, fmt.Errorf("build clientset for reconciling cluster %q: %w", rc.Name, err)
	}
	return &reconcilingClusterHandle{Client: c, Clientset: cs}, nil
}

// invalidateReconcilingClusterClientByName drops any cached client for the
// reconciling cluster named name — called after a kubeconfig rotation
// (handleCreateReconcilingCluster's own re-POST path) so a stale cached
// client (built from the now-superseded kubeconfig) isn't kept serving
// requests against credentials that may have just been revoked. A lookup
// miss (nothing cached yet, or the cluster is unknown) is a silent no-op —
// nothing to invalidate.
func (s *Server) invalidateReconcilingClusterClientByName(ctx context.Context, name string) {
	rc, err := s.OrgStore.GetReconcilingClusterByName(ctx, name)
	if err != nil {
		return
	}
	s.reconcilingClientsMu.Lock()
	delete(s.reconcilingClusterClients, rc.ID)
	s.reconcilingClientsMu.Unlock()
}

// SweepReconcilingClusterHealth runs one reachability check per registered
// reconciling cluster, writing the result back via
// Store.SetReconcilingClusterHealth — the periodic half of Milestone 6's
// own health-check design (see HYVE-ORGANIZATION-MODEL-PROPOSAL.md).
// Deliberately API-server-owned, never the controller (which never touches
// Store at all — see NamespaceReconciler's own doc comment for the same
// boundary). Meant to be called on a recurring interval by cmd/api/run.go.
// A discovery client's cheap, unauthenticated-safe /version call is the
// reachability probe — built fresh each tick from the same kubeconfig
// Secret resourceClient itself reads, deliberately not reusing the cached
// client.Client (a different client type; nothing else needs it kept
// around).
func (s *Server) SweepReconcilingClusterHealth(ctx context.Context) {
	clusters, err := s.OrgStore.ListReconcilingClusters(ctx)
	if err != nil {
		log.Printf("api: failed to list reconciling clusters for health check: %v", err)
		return
	}
	for _, rc := range clusters {
		reachable, checkErr := s.checkReconcilingClusterHealth(ctx, rc)
		if err := s.OrgStore.SetReconcilingClusterHealth(ctx, rc.ID, reachable, checkErr); err != nil {
			log.Printf("api: failed to record health for reconciling cluster %q: %v", rc.Name, err)
		}
	}
}

func (s *Server) checkReconcilingClusterHealth(ctx context.Context, rc orgdb.ReconcilingCluster) (bool, error) {
	var secret corev1.Secret
	key := types.NamespacedName{Namespace: rc.KubeconfigSecretNamespace, Name: rc.KubeconfigSecretName}
	if err := s.Client.Get(ctx, key, &secret); err != nil {
		return false, fmt.Errorf("get kubeconfig secret: %w", err)
	}
	kubeconfigBytes, ok := secret.Data[reconcilingClusterKubeconfigSecretKey]
	if !ok {
		return false, fmt.Errorf("kubeconfig secret has no %q key", reconcilingClusterKubeconfigSecretKey)
	}
	restCfg, err := clientcmd.RESTConfigFromKubeConfig(kubeconfigBytes)
	if err != nil {
		return false, fmt.Errorf("parse kubeconfig: %w", err)
	}
	disco, err := discovery.NewDiscoveryClientForConfig(restCfg)
	if err != nil {
		return false, fmt.Errorf("build discovery client: %w", err)
	}
	if _, err := disco.ServerVersion(); err != nil {
		return false, fmt.Errorf("server version check failed: %w", err)
	}
	return true, nil
}
