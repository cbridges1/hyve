package api

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strconv"
	"time"

	hyvev1alpha1 "github.com/cbridges1/hyve/internal/apis/hyve/v1alpha1"
	"github.com/cbridges1/hyve/internal/template"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// clusterActivityDTO is GET /clusters/<name>/events's response shape — the
// only place a create/delete script's actual output survives at all
// (k8sjob.Run always deletes its dispatched Job immediately after fetching
// logs), plus the Kubernetes Events a reconcile emits at each lifecycle
// milestone (see internal/reconcile.ReconcileHooks/internal/controller.
// ClusterDefinitionReconciler.Recorder) — bundled into one response since a
// caller asking "what happened to this cluster" wants both together, not two
// round trips. Events is paginated (?limit=/?offset=, see
// handleGetClusterEvents) — a long-lived cluster reconciled every few
// minutes for days accumulates far more events than any UI should render
// unbounded in one page; TotalEvents lets a caller render "X-Y of Z"
// without a second round trip.
type clusterActivityDTO struct {
	Events           []clusterEventDTO `json:"events"`
	TotalEvents      int               `json:"totalEvents"`
	LastCreateOutput string            `json:"lastCreateOutput,omitempty"`
	LastDeleteOutput string            `json:"lastDeleteOutput,omitempty"`
}

type clusterEventDTO struct {
	Type     string `json:"type"`
	Reason   string `json:"reason"`
	Message  string `json:"message"`
	Count    int32  `json:"count"`
	LastSeen string `json:"lastSeen"`
}

// clusterDTO is the response shape for GET /api/clusters and
// GET /api/clusters/<name> — deliberately excludes driverOutputs and any
// kubeconfig data, see HYVE-CONTROLLER-ARCHITECTURE-PLAN.md's Phase 6.4.
type clusterDTO struct {
	// Name is always the short, user-facing name — cd.Name itself
	// (metadata.name) when Environment is empty (legacy/unmanaged
	// namespace), or cd.Name with its "<Environment>-" prefix stripped
	// when it's set. See internal/api/environmentnaming.go's own doc
	// comments for why this direction (given the environment) is safe,
	// unlike blindly reverse-parsing an arbitrary name.
	Name string `json:"name"`
	// Environment is the hyve.io/environment label's value, empty when
	// unset (every object created before Milestone 3, or in a namespace
	// with no matching Organization — see resolveResourceEnvironment).
	Environment        string             `json:"environment,omitempty"`
	Driver             string             `json:"driver"`
	Conditions         []metav1.Condition `json:"conditions,omitempty"`
	ObservedGeneration int64              `json:"observedGeneration"`
	AccessMethod       string             `json:"accessMethod,omitempty"`
	AccessLastMinted   string             `json:"accessLastMinted,omitempty"`

	// Agent surfaces spec.access.agent (nil when unset — Proxy alone
	// meaningfully requires Enabled, so a caller distinguishing "never
	// configured" from "explicitly false" needs the nil case preserved,
	// unlike AccessMethod's always-present string) so `hyve cluster auth`
	// can recognize the agent-proxy path before deciding which message to
	// print (see cmd/cluster/auth.go), and so `hyve cluster show`/the web
	// console can display whether it's configured at all.
	Agent *hyvev1alpha1.AgentSpec `json:"agent,omitempty"`

	// AgentStatus surfaces status.agent — hyve-agent's live connectivity
	// snapshot, written directly by whichever hyve-api process holds this
	// cluster's tunnel connection (internal/api/agent_listener.go), not
	// the reconciler. Value type, not pointer, mirroring
	// ClusterDefinitionStatus.Agent's own shape exactly (Connected: false
	// is a meaningful, always-present value, not "unset" — see that
	// field's own doc comment on why it deliberately has no omitempty).
	AgentStatus hyvev1alpha1.AgentStatus `json:"agentStatus,omitempty"`

	// PendingDeletion reflects metadata.deletionTimestamp != nil — DELETE
	// /clusters/<name> only ever sets this (see handleDeleteCluster), it
	// never removes the object outright: ClusterDefinitionFinalizer keeps
	// it around, visible via GET, until the controller finishes OnDelete
	// hooks + the driver's own delete + AfterDelete and removes the
	// finalizer itself — confirmed live, this could otherwise sit
	// unnoticed for a full reconcile cycle with nothing in the API
	// response to tell a caller a delete is even in flight.
	PendingDeletion bool `json:"pendingDeletion,omitempty"`

	// Spec is the full declared spec — added for PATCH /clusters/<name>'s
	// sake (the web console's generic edit panel needs to seed itself with
	// the current spec, same as templateDTO/workflowDTO/resourceDTO already
	// do). Not the same concern the doc comment above warns about: that's
	// about *status*-level data (driverOutputs, kubeconfig-equivalent
	// results), not the user-declared spec, which is no more sensitive here
	// than it already is on every other exposed CRD type.
	Spec *hyvev1alpha1.ClusterDefinitionSpec `json:"spec,omitempty"`
}

func toClusterDTO(cd *hyvev1alpha1.ClusterDefinition) clusterDTO {
	spec := cd.Spec
	name, environment := cd.Name, cd.Labels[hyveEnvironmentLabel]
	if environment != "" {
		name = splitEnvironmentPrefix(cd.Name, environment)
	}
	return clusterDTO{
		Name:               name,
		Environment:        environment,
		Driver:             cd.Spec.Driver.Source,
		Conditions:         cd.Status.Conditions,
		ObservedGeneration: cd.Status.ObservedGeneration,
		PendingDeletion:    cd.DeletionTimestamp != nil,
		Spec:               &spec,
		// Spec, not Status: this reflects the *declared* access method
		// (module-auth/tunnel/primary), always known immediately — Status.
		// Access.Method is a separate, rarely-populated status echo (see
		// AccessStatus's own doc comment) that every real consumer of this
		// DTO field (the web UI's host-cluster badge, cmd/migrate_resolve.go's
		// current-host lookup, cmd/cluster/api.go's display) actually needs
		// the spec value for — confirmed live: reading Status here left the
		// UI badge never showing and migrate's host-resolution never
		// matching, for every access method including the new primary one.
		AccessMethod:     cd.Spec.Access.Method,
		AccessLastMinted: cd.Status.Access.LastMinted,
		Agent:            cd.Spec.Access.Agent,
		AgentStatus:      cd.Status.Agent,
	}
}

// registerClusterRoutes wires the /clusters endpoints onto mux — mounted
// under /api/ (and behind requireAuth+requireRole) by Server.Routes.
func (s *Server) registerClusterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /clusters", s.requireOrganizationNotMigrating(s.handleListClusters))
	mux.HandleFunc("GET /clusters/{name}", s.requireOrganizationNotMigrating(s.handleGetCluster))
	mux.HandleFunc("POST /clusters", s.requireOrganizationNotMigrating(s.handleCreateCluster))
	mux.HandleFunc("PATCH /clusters/{name}", s.requireOrganizationNotMigrating(s.handleUpdateCluster))
	mux.HandleFunc("DELETE /clusters/{name}", s.requireOrganizationNotMigrating(s.handleDeleteCluster))
	mux.HandleFunc("GET /clusters/{name}/resources", s.requireOrganizationNotMigrating(s.handleGetClusterResources))
	mux.HandleFunc("GET /clusters/{name}/events", s.requireOrganizationNotMigrating(s.handleGetClusterEvents))
}

func (s *Server) handleListClusters(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	namespace := s.TenantNamespace(r)
	rc, err := s.resourceClient(ctx, namespace)
	if err != nil {
		log.Printf("api: failed to resolve reconciling cluster for %q: %v", namespace, err)
		writeError(w, http.StatusInternalServerError, "failed to list clusters")
		return
	}
	var list hyvev1alpha1.ClusterDefinitionList
	// client.InNamespace is required, not optional: ClusterDefinition is a
	// namespaced resource, but hyve-api's own Role (deploy/helm/hyve-api's
	// rbac.yaml) is deliberately namespace-scoped too, not a ClusterRole —
	// an unscoped List() call is a genuine cluster-wide list attempt, which
	// that Role can never satisfy regardless of what it grants within
	// s.Namespace. Confirmed live: this exact mismatch 500'd every request
	// with no server-side log line at all until the error below was added.
	if err := rc.List(ctx, &list, client.InNamespace(namespace)); err != nil {
		log.Printf("api: failed to list clusters: %v", err)
		writeError(w, http.StatusInternalServerError, "failed to list clusters")
		return
	}
	dtos := make([]clusterDTO, 0, len(list.Items))
	for i := range list.Items {
		dto := toClusterDTO(&list.Items[i])
		dto.Environment = s.effectiveEnvironmentLabel(ctx, namespace, dto.Environment)
		dtos = append(dtos, dto)
	}
	writeJSON(w, http.StatusOK, dtos)
}

func (s *Server) handleGetCluster(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	namespace := s.TenantNamespace(r)
	name := s.resolveAddressedName(r, namespace, r.PathValue("name"))
	rc, err := s.resourceClient(ctx, namespace)
	if err != nil {
		log.Printf("api: failed to resolve reconciling cluster for %q: %v", namespace, err)
		writeError(w, http.StatusInternalServerError, "failed to get cluster")
		return
	}
	var cd hyvev1alpha1.ClusterDefinition
	if err := rc.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, &cd); err != nil {
		if apierrors.IsNotFound(err) {
			writeError(w, http.StatusNotFound, "cluster not found")
			return
		}
		log.Printf("api: failed to get cluster %q: %v", name, err)
		writeError(w, http.StatusInternalServerError, "failed to get cluster")
		return
	}
	dto := toClusterDTO(&cd)
	dto.Environment = s.effectiveEnvironmentLabel(ctx, namespace, dto.Environment)
	writeJSON(w, http.StatusOK, dto)
}

// clusterResourcesDTO is a separate endpoint (not folded into clusterDTO)
// specifically so GET /clusters and the base GET /clusters/<name> stay
// lean — a cluster can declare/track an arbitrary number of resources, and
// most callers of those two endpoints don't need them.
type clusterResourcesDTO struct {
	Resources        []hyvev1alpha1.ResourceRef               `json:"resources,omitempty"`
	AppliedResources map[string]*hyvev1alpha1.AppliedResource `json:"appliedResources,omitempty"`
}

func (s *Server) handleGetClusterResources(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	namespace := s.TenantNamespace(r)
	name := s.resolveAddressedName(r, namespace, r.PathValue("name"))
	rc, err := s.resourceClient(ctx, namespace)
	if err != nil {
		log.Printf("api: failed to resolve reconciling cluster for %q: %v", namespace, err)
		writeError(w, http.StatusInternalServerError, "failed to get cluster")
		return
	}
	var cd hyvev1alpha1.ClusterDefinition
	if err := rc.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, &cd); err != nil {
		if apierrors.IsNotFound(err) {
			writeError(w, http.StatusNotFound, "cluster not found")
			return
		}
		log.Printf("api: failed to get cluster %q: %v", name, err)
		writeError(w, http.StatusInternalServerError, "failed to get cluster")
		return
	}
	writeJSON(w, http.StatusOK, clusterResourcesDTO{Resources: cd.Spec.Resources, AppliedResources: cd.Status.AppliedResources})
}

// handleGetClusterEvents answers "I see no logs that indicate the job
// responsible for creating/deleting this cluster does anything" — until
// this endpoint existed there was genuinely nowhere to look: k8sjob.Run
// always deletes its dispatched Job immediately after fetching logs, and the
// module operation's own raw stdout was previously discarded entirely,
// never even reaching log.Printf (see module.Executor.executeScript before
// OperationResult.RawOutput existed). Uses s.Clientset (a raw client-go
// Interface, not the cached controller-runtime s.Client) specifically so
// the involvedObject.name field selector below is evaluated by the real API
// server — a cached client has no such capability without a manager-level
// field indexer this API server doesn't register.
// defaultClusterEventsLimit/maxClusterEventsLimit bound ?limit= — default
// keeps the common case (no query params at all, e.g. an older CLI/UI
// build) from ever rendering an unbounded list; max keeps a caller from
// requesting the whole event history in one response regardless.
const (
	defaultClusterEventsLimit = 20
	maxClusterEventsLimit     = 200
)

// maxTrackedClusterEvents caps how many of a ClusterDefinition's Kubernetes
// Events this endpoint ever considers, after sorting newest-first — a
// cluster reconciled every few seconds (e.g. the host cluster, polled by
// the UI every 5s) can accumulate far more Event objects within
// Kubernetes' own event-ttl window (apiserver default 1h) than any "recent
// activity" feed should hold: confirmed live, this list growing large
// enough that requesting it repeatedly became its own source of load.
// Applied before pagination, so TotalEvents/offset/limit all operate
// against this capped set, not the true (unbounded) count — the oldest
// events beyond the cap are simply never shown, which is the point.
const maxTrackedClusterEvents = 500

func (s *Server) handleGetClusterEvents(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	ns := s.TenantNamespace(r)
	name := s.resolveAddressedName(r, ns, r.PathValue("name"))

	limit := parseClusterEventsIntParam(r, "limit", defaultClusterEventsLimit, 1, maxClusterEventsLimit)
	offset := parseClusterEventsIntParam(r, "offset", 0, 0, 1<<30)

	rc, err := s.resourceClient(ctx, ns)
	if err != nil {
		log.Printf("api: failed to resolve reconciling cluster for %q: %v", ns, err)
		writeError(w, http.StatusInternalServerError, "failed to get cluster")
		return
	}
	var cd hyvev1alpha1.ClusterDefinition
	if err := rc.Get(ctx, types.NamespacedName{Namespace: ns, Name: name}, &cd); err != nil {
		if apierrors.IsNotFound(err) {
			writeError(w, http.StatusNotFound, "cluster not found")
			return
		}
		log.Printf("api: failed to get cluster %q: %v", name, err)
		writeError(w, http.StatusInternalServerError, "failed to get cluster")
		return
	}

	dto := clusterActivityDTO{
		Events:           []clusterEventDTO{},
		LastCreateOutput: cd.Status.LastCreateOutput,
		LastDeleteOutput: cd.Status.LastDeleteOutput,
	}

	clientset, err := s.resourceClientset(ctx, ns)
	if err != nil {
		log.Printf("api: failed to resolve reconciling cluster clientset for %q: %v", ns, err)
		writeError(w, http.StatusInternalServerError, "failed to list cluster events")
		return
	}
	if clientset != nil {
		selector := fmt.Sprintf("involvedObject.kind=ClusterDefinition,involvedObject.name=%s", name)
		list, err := clientset.CoreV1().Events(ns).List(ctx, metav1.ListOptions{FieldSelector: selector})
		if err != nil {
			log.Printf("api: failed to list events for cluster %q: %v", name, err)
			writeError(w, http.StatusInternalServerError, "failed to list cluster events")
			return
		}
		for _, ev := range list.Items {
			lastSeen := ev.LastTimestamp.Time
			if lastSeen.IsZero() {
				lastSeen = ev.EventTime.Time
			}
			dto.Events = append(dto.Events, clusterEventDTO{
				Type:     ev.Type,
				Reason:   ev.Reason,
				Message:  ev.Message,
				Count:    ev.Count,
				LastSeen: lastSeen.Format(time.RFC3339),
			})
		}
		// Newest first — a "recent activity" feed with the oldest entries
		// first (this package's original sort order) buries exactly what a
		// caller opened the page to see, and is compounded by unbounded
		// length: on a long-lived cluster reconciled every few minutes for
		// days, the newest, most relevant events could be pages away.
		sort.Slice(dto.Events, func(i, j int) bool { return dto.Events[i].LastSeen > dto.Events[j].LastSeen })
		if len(dto.Events) > maxTrackedClusterEvents {
			dto.Events = dto.Events[:maxTrackedClusterEvents]
		}

		dto.TotalEvents = len(dto.Events)
		if offset > len(dto.Events) {
			offset = len(dto.Events)
		}
		end := offset + limit
		if end > len(dto.Events) {
			end = len(dto.Events)
		}
		dto.Events = dto.Events[offset:end]
	}

	writeJSON(w, http.StatusOK, dto)
}

// parseClusterEventsIntParam parses r's query param name as a non-negative
// int, clamped to [min, max] — an invalid or missing value silently falls
// back to def rather than erroring the whole request over a malformed
// pagination param.
func parseClusterEventsIntParam(r *http.Request, name string, def, min, max int) int {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return def
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		return def
	}
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}

// createClusterRequest reuses hyvev1alpha1.ClusterDefinitionSpec directly
// as the request body's spec shape — the Kubernetes API server's own CRD
// schema validation on the Create call below is "the CRD's own OpenAPI"
// validation the plan asks for; no second hand-rolled validator needed.
// Template is mutually exclusive with Spec: when set, the named Template CR
// is fetched and rendered into a spec via the same
// hyvev1alpha1.RenderClusterDefinitionSpec function
// POST /templates/{name}/render uses standalone — this is the one-round-trip
// path for the common "create a cluster from a template" case; Spec set
// directly (today's only option) still works unchanged.
type createClusterRequest struct {
	Name     string                             `json:"name"`
	Spec     hyvev1alpha1.ClusterDefinitionSpec `json:"spec,omitempty"`
	Template *createClusterFromTemplateRef      `json:"template,omitempty"`
}

// createClusterFromTemplateRef names a Template CR plus the same
// region/param overrides renderTemplateRequest accepts.
type createClusterFromTemplateRef struct {
	Name   string            `json:"name"`
	Region string            `json:"region,omitempty"`
	Params map[string]string `json:"params,omitempty"`
}

func (s *Server) handleCreateCluster(w http.ResponseWriter, r *http.Request) {
	if !RequireRole(w, r, hyvev1alpha1.RoleAdmin) {
		return
	}
	var req createClusterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}

	namespace := s.TenantNamespace(r)
	rc, err := s.resourceClient(r.Context(), namespace)
	if err != nil {
		log.Printf("api: failed to resolve reconciling cluster for %q: %v", namespace, err)
		writeError(w, http.StatusInternalServerError, "failed to create cluster")
		return
	}

	spec := req.Spec
	if req.Template != nil {
		var tpl hyvev1alpha1.Template
		if err := rc.Get(r.Context(), types.NamespacedName{Namespace: namespace, Name: req.Template.Name}, &tpl); err != nil {
			if apierrors.IsNotFound(err) {
				writeError(w, http.StatusNotFound, fmt.Sprintf("template %q not found", req.Template.Name))
				return
			}
			log.Printf("api: failed to get template %q: %v", req.Template.Name, err)
			writeError(w, http.StatusInternalServerError, "failed to get template")
			return
		}
		spec = hyvev1alpha1.RenderClusterDefinitionSpec(tpl.Spec, req.Template.Region, req.Template.Params)

		// RenderClusterDefinitionSpec doesn't compute this itself (it lives
		// in internal/apis/hyve/v1alpha1, which internal/template already
		// imports the other way — pulling CronNextOccurrence in there would
		// be an import cycle), so every caller has to do it explicitly.
		// cmd/cluster/create.go (local mode) already does; this cluster-mode
		// path never did, which meant a cluster created here from a
		// schedule-having template got no spec.expiresAt at all — expiry
		// (internal/reconcile's ReconcileOne) had nothing to ever act on, so
		// "scheduled deletion" silently never happened for any
		// cluster-mode-created cluster, regardless of how much time passed.
		if tpl.Spec.Schedule != "" {
			next, err := template.CronNextOccurrence(tpl.Spec.Schedule, time.Now())
			if err != nil {
				writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid schedule %q on template %q: %v", tpl.Spec.Schedule, req.Template.Name, err))
				return
			}
			spec.ExpiresAt = next.Format(time.RFC3339)
		}
	}

	envResult, ok := s.resolveCreateName(w, r, namespace, req.Name)
	if !ok {
		return
	}
	meta := metav1.ObjectMeta{Name: envResult.RealName, Namespace: namespace}
	if envResult.HasEnvironment {
		meta.Labels = map[string]string{hyveEnvironmentLabel: envResult.Label}
	}
	cd := &hyvev1alpha1.ClusterDefinition{
		ObjectMeta: meta,
		Spec:       spec,
	}
	if err := rc.Create(r.Context(), cd); err != nil {
		if apierrors.IsAlreadyExists(err) {
			writeError(w, http.StatusConflict, "cluster already exists")
			return
		}
		writeError(w, http.StatusBadRequest, fmt.Sprintf("failed to create cluster: %v", err))
		return
	}
	writeJSON(w, http.StatusCreated, toClusterDTO(cd))
}

// updateClusterRequest reuses hyvev1alpha1.ClusterDefinitionSpec directly,
// same precedent as createClusterRequest — a generic raw-spec PATCH, not a
// partial/merge-patch (the whole spec is replaced), matching this
// console's "a CR is just YAML" edit model across every type.
type updateClusterRequest struct {
	Spec hyvev1alpha1.ClusterDefinitionSpec `json:"spec"`
}

func (s *Server) handleUpdateCluster(w http.ResponseWriter, r *http.Request) {
	if !RequireRole(w, r, hyvev1alpha1.RoleAdmin) {
		return
	}
	ctx := r.Context()
	namespace := s.TenantNamespace(r)
	name := s.resolveAddressedName(r, namespace, r.PathValue("name"))
	var req updateClusterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	rc, err := s.resourceClient(ctx, namespace)
	if err != nil {
		log.Printf("api: failed to resolve reconciling cluster for %q: %v", namespace, err)
		writeError(w, http.StatusInternalServerError, "failed to update cluster")
		return
	}
	var cd hyvev1alpha1.ClusterDefinition
	if err := rc.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, &cd); err != nil {
		if apierrors.IsNotFound(err) {
			writeError(w, http.StatusNotFound, "cluster not found")
			return
		}
		log.Printf("api: failed to get cluster %q: %v", name, err)
		writeError(w, http.StatusInternalServerError, "failed to get cluster")
		return
	}
	cd.Spec = req.Spec
	if err := rc.Update(ctx, &cd); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("failed to update cluster: %v", err))
		return
	}
	dto := toClusterDTO(&cd)
	dto.Environment = s.effectiveEnvironmentLabel(ctx, namespace, dto.Environment)
	writeJSON(w, http.StatusOK, dto)
}

func (s *Server) handleDeleteCluster(w http.ResponseWriter, r *http.Request) {
	if !RequireRole(w, r, hyvev1alpha1.RoleAdmin) {
		return
	}
	ctx := r.Context()
	namespace := s.TenantNamespace(r)
	name := s.resolveAddressedName(r, namespace, r.PathValue("name"))
	rc, err := s.resourceClient(ctx, namespace)
	if err != nil {
		log.Printf("api: failed to resolve reconciling cluster for %q: %v", namespace, err)
		writeError(w, http.StatusInternalServerError, "failed to delete cluster")
		return
	}
	cd := &hyvev1alpha1.ClusterDefinition{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace}}
	if err := rc.Delete(ctx, cd); err != nil {
		if apierrors.IsNotFound(err) {
			writeError(w, http.StatusNotFound, "cluster not found")
			return
		}
		log.Printf("api: failed to delete cluster %q: %v", name, err)
		writeError(w, http.StatusInternalServerError, "failed to delete cluster")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
