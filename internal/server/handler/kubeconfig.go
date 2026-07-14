package handler

import (
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/cbridges1/hyve/internal/kubeconfig"
	mod "github.com/cbridges1/hyve/internal/module"
	"github.com/cbridges1/hyve/internal/types"
)

// KubeconfigHandlers backs the /clusters/:name/kubeconfig routes.
type KubeconfigHandlers struct {
	*Deps
}

func NewKubeconfigHandlers(deps *Deps) *KubeconfigHandlers { return &KubeconfigHandlers{deps} }

func moduleEnv(cluster *types.ClusterDefinition) []string {
	env := []string{
		"HYVE_CLUSTER_NAME=" + cluster.Metadata.Name,
		"HYVE_CLUSTER_REGION=" + cluster.Metadata.Region,
	}
	for k, v := range cluster.Spec.Params {
		env = append(env, "HYVE_PARAM_"+strings.ToUpper(k)+"="+v)
	}
	for k, v := range cluster.Spec.DriverOutputs {
		env = append(env, k+"="+v)
	}
	return env
}

type authKubeconfigRequest struct {
	Method string `json:"method,omitempty"`
}

// Auth handles POST /clusters/:name/kubeconfig — runs the module's auth
// operation (mirrors `hyve cluster auth <name> [--method]`), then returns the
// resulting ~/.kube/config content. Hyve's Go code never constructs the
// kubeconfig itself — the module's auth script (e.g. `aws eks
// update-kubeconfig`, civo's --save) writes it as a side effect; this reads
// it back afterward.
func (h *KubeconfigHandlers) Auth(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	var req authKubeconfigRequest
	if r.ContentLength != 0 {
		if !readJSON(w, r, &req) {
			return
		}
	}

	cluster, _, err := h.StateMgr.LoadClusterDefinition(name)
	if err != nil {
		if os.IsNotExist(err) {
			writeError(w, http.StatusNotFound, "cluster not found: "+name)
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	lf, err := mod.LoadLockFile(h.RepoPath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load hyve.lock: "+err.Error())
		return
	}
	locked := lf.GetLocked(cluster.Spec.Driver.Source, cluster.Spec.Driver.Version)
	if locked == nil && !mod.IsLocalSource(cluster.Spec.Driver.Source) {
		writeError(w, http.StatusBadRequest, "module "+cluster.Spec.Driver.Source+"@"+cluster.Spec.Driver.Version+" not in hyve.lock — run module install first")
		return
	}
	resolved, err := mod.Resolve(cluster.Spec.Driver.Source, cluster.Spec.Driver.Version, locked, h.RepoPath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to resolve module: "+err.Error())
		return
	}

	manifest, _ := mod.LoadManifestForSource(cluster.Spec.Driver.Source, cluster.Spec.Driver.Version, h.RepoPath, lf)
	if manifest != nil {
		if reqErr := mod.ValidateToolRequirements(manifest.Spec.Requirements.Tools); reqErr != nil {
			writeError(w, http.StatusBadRequest, reqErr.Error())
			return
		}
	}

	executor := &mod.Executor{ModuleDir: resolved.Dir, Env: moduleEnv(cluster), WorkDir: h.RepoPath, AuthMethod: req.Method}
	if _, err := executor.Execute(r.Context(), mod.OperationAuth); err != nil {
		writeError(w, http.StatusInternalServerError, "auth failed: "+err.Error())
		return
	}

	kubeconfigPath, err := mod.DefaultKubeconfigPath()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := kubeconfig.DeduplicateKubeconfigEntries(kubeconfigPath); err != nil {
		log.Printf("Warning: failed to deduplicate kubeconfig: %v", err)
	}
	kcData, err := os.ReadFile(kubeconfigPath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "auth succeeded but failed to read kubeconfig: "+err.Error())
		return
	}
	w.Header().Set("Content-Type", yamlMediaType)
	w.WriteHeader(http.StatusOK)
	w.Write(kcData)
}

// Deauth handles DELETE /clusters/:name/kubeconfig — mirrors `hyve cluster
// deauth <name>`: removes the context/cluster/user entries for this cluster
// from ~/.kube/config.
func (h *KubeconfigHandlers) Deauth(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	kubeconfigPath, err := mod.DefaultKubeconfigPath()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if _, err := os.Stat(kubeconfigPath); os.IsNotExist(err) {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	existingData, err := os.ReadFile(kubeconfigPath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := kubeconfig.RemoveKubeconfigContext(string(existingData), name, kubeconfigPath); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Sync handles POST /clusters/kubeconfig/sync?dryRun=true — mirrors `hyve
// cluster auth sync`: diffs ~/.kube/config context names against known
// cluster definitions and removes orphaned contexts (cluster+user included).
func (h *KubeconfigHandlers) Sync(w http.ResponseWriter, r *http.Request) {
	dryRun := r.URL.Query().Get("dryRun") == "true"

	clusterDefs, err := h.StateMgr.LoadClusterDefinitions()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	known := make(map[string]bool, len(clusterDefs))
	for _, c := range clusterDefs {
		known[c.Metadata.Name] = true
	}

	kubeconfigPath, err := mod.DefaultKubeconfigPath()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	data, err := os.ReadFile(kubeconfigPath)
	if err != nil {
		if os.IsNotExist(err) {
			writeJSON(w, http.StatusOK, map[string][]string{"removed": {}})
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	contextNames, err := kubeconfig.ContextNames(string(data))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	var orphans, removed []string
	for _, name := range contextNames {
		if !known[name] {
			orphans = append(orphans, name)
		}
	}
	for _, name := range orphans {
		if dryRun {
			removed = append(removed, name)
			continue
		}
		if err := kubeconfig.RemoveKubeconfigContext(string(data), name, kubeconfigPath); err != nil {
			continue // best-effort, matches CLI's log-and-continue
		}
		removed = append(removed, name)
		data, _ = os.ReadFile(kubeconfigPath) // same re-read requirement as the CLI
	}
	writeJSON(w, http.StatusOK, map[string][]string{"removed": removed})
}
