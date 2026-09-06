package api

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	hyvev1alpha1 "github.com/cbridges1/hyve/internal/apis/hyve/v1alpha1"
	"github.com/cbridges1/hyve/internal/k8sjob"
	"github.com/cbridges1/hyve/internal/module"

	"gopkg.in/yaml.v3"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

const (
	// mintTimeout bounds how long handleAccessMethodMint's own HTTP
	// request waits for the dispatched Job to push a result — well under
	// mintJobActiveDeadline, so a caller that gives up here is never left
	// wondering whether the Job might still complete afterward.
	mintTimeout           = 90 * time.Second
	mintJobActiveDeadline = int64(120)

	// maxMintResultBytes caps what handleAccessMethodMintRelay will read
	// from a push — plenty for any real kubeconfig, and a hard ceiling
	// against a runaway or malicious POST to the relay endpoint.
	maxMintResultBytes = 1 << 20 // 1MiB

	// hyveConfigSingletonName matches cmd/controller's own --config-name
	// default — the API has no equivalent flag of its own today, so this
	// is the one name defaultModuleImage's best-effort lookup checks.
	hyveConfigSingletonName = "hyve-config"
)

// mintPendingEntry is one in-flight access-method mint request awaiting a
// push from its dispatched Job's callback (see handleAccessMethodMint/
// handleAccessMethodMintRelay) — registered in Server.mintPending, keyed
// by a random request ID, for exactly as long as one mint request is
// outstanding.
type mintPendingEntry struct {
	token  string
	result chan mintPushResult
}

// mintPushResult is exactly what the Job's wrapper script (see
// buildMintWrapperScript) POSTs to the relay endpoint: Status is "ok"
// (Kubeconfig set) or "error" (Message set), never both.
type mintPushResult struct {
	Status     string
	Kubeconfig string
	Message    string
}

type mintAccessMethodRequest struct {
	ClusterName           string            `json:"clusterName"`
	AccessMethodClusterID string            `json:"accessMethodClusterID"`
	CredentialEnv         map[string]string `json:"credentialEnv"`
}

type mintAccessMethodResponse struct {
	Kubeconfig string `json:"kubeconfig"`
}

// registerAccessMethodMintRoutes wires POST /access-methods/{name}/mint —
// mounted under /api/ (behind requireAuth+requireRole) by Server.Routes.
func (s *Server) registerAccessMethodMintRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /access-methods/{name}/mint", s.handleAccessMethodMint)
}

// registerAccessMethodMintRelayRoutes wires POST /relay/{id} — mounted on
// Server.RelayRoutes' own separate, Ingress-less listener, never under
// /api/.
func (s *Server) registerAccessMethodMintRelayRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /relay/{id}", s.handleAccessMethodMintRelay)
}

// handleAccessMethodMint resolves the named AccessMethod's driver module
// server-side, dispatches its auth operation to a short-lived Job running
// AccessMethod.Spec.Runner.Image, and waits for that Job to actively push
// its result back over the internal relay listener (RelayRoutes) — never
// by polling Job status and reading pod logs, which would put the
// resulting kubeconfig in a log stream that persists on-node (and in any
// log aggregator) well past the Job's own lifetime. See
// HYVE-ACCESS-METHOD-DESIGN.md's "Server-side dispatch" section for the
// full design and its one residual, structural risk: the caller's own
// credential (req.CredentialEnv) still has to reach the Job somehow, and
// while it's carried in a short-lived, owner-referenced Secret rather than
// a literal pod-spec env var, that Secret is a real Kubernetes object for
// the Job's brief lifetime — narrower exposure than the alternatives, not
// zero.
func (s *Server) handleAccessMethodMint(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")

	if s.Clientset == nil {
		writeError(w, http.StatusInternalServerError, "access-method minting is not configured on this API (no Kubernetes clientset)")
		return
	}
	if s.RelayBaseURL == "" {
		writeError(w, http.StatusInternalServerError, "access-method minting is not configured on this API (no relay base URL)")
		return
	}

	var req mintAccessMethodRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.ClusterName == "" || req.AccessMethodClusterID == "" {
		writeError(w, http.StatusBadRequest, "clusterName and accessMethodClusterID are required")
		return
	}

	tenantNS := s.TenantNamespace(r)

	var am hyvev1alpha1.AccessMethod
	if err := s.Client.Get(r.Context(), types.NamespacedName{Namespace: tenantNS, Name: name}, &am); err != nil {
		if apierrors.IsNotFound(err) {
			writeError(w, http.StatusNotFound, "access method not found")
			return
		}
		log.Printf("api: failed to get access method %q: %v", name, err)
		writeError(w, http.StatusInternalServerError, "failed to get access method")
		return
	}

	authScript, err := s.resolveAccessMethodAuthScript(r.Context(), name, &am)
	if err != nil {
		log.Printf("api: failed to resolve access method %q's auth script: %v", name, err)
		writeError(w, http.StatusInternalServerError, "failed to resolve access method auth script")
		return
	}

	requestID, err := randomHex(16)
	if err != nil {
		log.Printf("api: failed to generate mint request id: %v", err)
		writeError(w, http.StatusInternalServerError, "failed to prepare access method mint request")
		return
	}
	relayToken, err := randomHex(32)
	if err != nil {
		log.Printf("api: failed to generate mint relay token: %v", err)
		writeError(w, http.StatusInternalServerError, "failed to prepare access method mint request")
		return
	}

	entry := &mintPendingEntry{token: relayToken, result: make(chan mintPushResult, 1)}
	s.mintPending.Store(requestID, entry)
	defer s.mintPending.Delete(requestID)

	env := []string{
		"HYVE_CLUSTER_NAME=" + req.ClusterName,
		"HYVE_ACCESS_METHOD_SERVER_URL=" + am.Spec.ServerURL,
		"HYVE_ACCESS_METHOD_CLUSTER_ID=" + req.AccessMethodClusterID,
		"HYVE_RELAY_URL=" + strings.TrimRight(s.RelayBaseURL, "/") + "/relay/" + requestID,
		"HYVE_RELAY_TOKEN=" + relayToken,
	}

	secretName := "hyve-am-mint-" + requestID

	image := am.Spec.Runner.Image
	if image == "" {
		image = s.defaultModuleImage(r.Context())
	}

	createdJobName, jobUID, err := k8sjob.PushJob(r.Context(), s.Clientset, k8sjob.PushJobRequest{
		Name:                  "am-mint-" + name,
		Namespace:             tenantNS,
		Image:                 image,
		Script:                buildMintWrapperScript(authScript),
		Env:                   env,
		SecretEnvFromName:     secretName,
		ActiveDeadlineSeconds: mintJobActiveDeadline,
	})
	if err != nil {
		log.Printf("api: failed to create mint job for access method %q: %v", name, err)
		writeError(w, http.StatusInternalServerError, "failed to dispatch access method job")
		return
	}

	cleanup := func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		policy := metav1.DeletePropagationBackground
		_ = s.Clientset.BatchV1().Jobs(tenantNS).Delete(cleanupCtx, createdJobName, metav1.DeleteOptions{PropagationPolicy: &policy})
	}

	// The Secret is created only after the Job, so its ownerReference can
	// point at a real, already-issued Job UID — Kubernetes doesn't
	// validate a Job's envFrom.secretRef at admission time, only at
	// container start, so the brief window where the Job exists but the
	// Secret doesn't just means the pod pends in
	// CreateContainerConfigError until it appears (a few seconds,
	// bounded well within ActiveDeadlineSeconds either way). This ordering
	// is what lets the credential Secret be garbage-collected by
	// Kubernetes itself the moment the Job is deleted, even if this
	// process crashes before its own cleanup() ever runs.
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: tenantNS,
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: "batch/v1",
				Kind:       "Job",
				Name:       createdJobName,
				UID:        jobUID,
			}},
		},
		StringData: req.CredentialEnv,
	}
	if _, err := s.Clientset.CoreV1().Secrets(tenantNS).Create(r.Context(), secret, metav1.CreateOptions{}); err != nil {
		log.Printf("api: failed to create mint credentials secret for access method %q: %v", name, err)
		cleanup()
		writeError(w, http.StatusInternalServerError, "failed to dispatch access method job")
		return
	}

	timeout := s.MintTimeout
	if timeout <= 0 {
		timeout = mintTimeout
	}

	var result mintPushResult
	select {
	case result = <-entry.result:
	case <-time.After(timeout):
		result = mintPushResult{Status: "error", Message: s.describeMintTimeout(r.Context(), tenantNS, createdJobName)}
	case <-r.Context().Done():
		cleanup()
		return
	}

	cleanup()

	if result.Status != "ok" {
		msg := result.Message
		if msg == "" {
			msg = "access method job did not report a result"
		}
		writeError(w, http.StatusBadGateway, msg)
		return
	}
	writeJSON(w, http.StatusOK, mintAccessMethodResponse{Kubeconfig: result.Kubeconfig})
}

// handleAccessMethodMintRelay is the one thing an access-method mint
// Job's wrapper script ever calls — see buildMintWrapperScript. No hyve
// session concept applies here at all (this listener isn't even served on
// the same port as /api/ — see RelayRoutes); the per-request relay token
// generated in handleAccessMethodMint is the entire authorization model,
// checked in constant time, matched to a specific pending request ID, and
// consumed exactly once.
func (s *Server) handleAccessMethodMintRelay(w http.ResponseWriter, r *http.Request) {
	requestID := r.PathValue("id")
	v, ok := s.mintPending.Load(requestID)
	if !ok {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	entry := v.(*mintPendingEntry)

	const prefix = "Bearer "
	authHeader := r.Header.Get("Authorization")
	if !strings.HasPrefix(authHeader, prefix) ||
		subtle.ConstantTimeCompare([]byte(strings.TrimPrefix(authHeader, prefix)), []byte(entry.token)) != 1 {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, maxMintResultBytes))
	if err != nil {
		http.Error(w, "failed to read body", http.StatusBadRequest)
		return
	}

	result := mintPushResult{}
	if r.Header.Get("X-Hyve-Status") == "ok" {
		result.Status = "ok"
		result.Kubeconfig = string(body)
	} else {
		result.Status = "error"
		result.Message = string(body)
	}

	// One-shot: remove before pushing, so anything racing this (the
	// wrapper script only ever sends one POST, but this guards the
	// invariant regardless) finds nothing and 404s rather than clobbering
	// an already-delivered result.
	s.mintPending.Delete(requestID)
	select {
	case entry.result <- result:
	default:
	}
	w.WriteHeader(http.StatusOK)
}

// resolveAccessMethodAuthScript returns the shell script to run for am's
// auth operation — either am.Spec.InlineAuth verbatim (no module
// resolution of any kind: no git clone, no ModulesDir/local-path lookup),
// or am.Spec.Driver resolved server-side against the API's own module
// cache, exactly as the previous (module-only) design always did. Exactly
// one of InlineAuth/Driver must be set; validated here rather than at the
// CRD-schema level (kubebuilder oneOf across two top-level fields is
// possible but adds real complexity for a check this simple to do in Go).
func (s *Server) resolveAccessMethodAuthScript(ctx context.Context, name string, am *hyvev1alpha1.AccessMethod) (string, error) {
	hasInline := am.Spec.InlineAuth != ""
	hasDriver := am.Spec.Driver.Source != ""
	switch {
	case hasInline && hasDriver:
		return "", fmt.Errorf("access method %q sets both inlineAuth and driver — exactly one must be set", name)
	case hasInline:
		return am.Spec.InlineAuth, nil
	case !hasDriver:
		return "", fmt.Errorf("access method %q sets neither inlineAuth nor driver", name)
	}

	lf, err := module.LoadLockFile(s.ModulesDir)
	if err != nil {
		return "", fmt.Errorf("load hyve.lock: %w", err)
	}
	locked := lf.GetLocked(am.Spec.Driver.Source, am.Spec.Driver.Version)
	resolved, err := module.ResolveWithToken(am.Spec.Driver.Source, am.Spec.Driver.Version, locked, s.ModulesDir, s.githubToken(ctx))
	if err != nil {
		return "", fmt.Errorf("resolve driver module %s@%s: %w", am.Spec.Driver.Source, am.Spec.Driver.Version, err)
	}
	authPath, ok := module.FindOperationFile(resolved.Dir, module.OperationAuth)
	if !ok {
		return "", fmt.Errorf("driver module %s has no auth operation", am.Spec.Driver.Source)
	}
	authContent, err := os.ReadFile(authPath)
	if err != nil {
		return "", fmt.Errorf("read auth file %s: %w", authPath, err)
	}
	return extractAuthScript(authPath, authContent)
}

// extractAuthScript returns the actual shell script an auth operation
// file names — mirrors internal/module.Executor's own dispatch (Execute/
// executeAuth): a plain .sh (or extensionless) file's content already IS
// the script; a .yaml file is a `kind: ClusterAuth` manifest whose
// spec.methods[0].auth.script (falling back to the legacy single-method
// spec.bootstrap.script) is what actually runs. Access-method job dispatch
// always uses the first/default method — there is no --method selection
// for this path, same "server always uses the module's default" stance
// cmd/cluster/auth.go's authClusterAPI doc comment already establishes for
// the other (tunnel/module-auth-override) server-side path.
func extractAuthScript(path string, content []byte) (string, error) {
	if !strings.HasSuffix(strings.ToLower(filepath.Base(path)), ".yaml") &&
		!strings.HasSuffix(strings.ToLower(filepath.Base(path)), ".yml") {
		return string(content), nil
	}

	var ca module.ClusterAuth
	if err := yaml.Unmarshal(content, &ca); err != nil {
		return "", fmt.Errorf("parse ClusterAuth %s: %w", path, err)
	}
	if len(ca.Spec.Methods) > 0 {
		if ca.Spec.Methods[0].Auth.Script == "" {
			return "", fmt.Errorf("%s: methods[0].auth.script is empty", path)
		}
		return ca.Spec.Methods[0].Auth.Script, nil
	}
	if ca.Spec.Bootstrap.Script == "" {
		return "", fmt.Errorf("%s: no methods and no bootstrap.script", path)
	}
	return ca.Spec.Bootstrap.Script, nil
}

// buildMintWrapperScript wraps an AccessMethod driver module's auth
// operation script (authScript, the module author's own, completely
// unmodified) so that whatever it writes to $KUBECONFIG — or its own
// failure — gets actively pushed to the relay listener via
// HYVE_RELAY_URL/HYVE_RELAY_TOKEN (both injected as plain env vars by
// handleAccessMethodMint) and the container then exits. Never printed to
// stdout/logs; never requires the pod to wait or sleep for anything to
// come read it — the push itself is the terminal action. The module
// author has no idea this wrapping exists, same "invisible" precedent
// internal/module.Executor's own Job-dispatch sentinel-marker wrapping
// already set for a different (log-based) relay mechanism. Requires curl
// in the runner image — an explicit, documented requirement of
// AccessMethodSpec.Runner.Image, alongside whatever the module itself
// needs.
func buildMintWrapperScript(authScript string) string {
	return fmt.Sprintf(`export KUBECONFIG=/tmp/hyve-access-method-kubeconfig
set +e
(
%s
)
__hyve_rc=$?
if [ $__hyve_rc -eq 0 ] && [ -s "$KUBECONFIG" ]; then
  curl -sf -X POST "$HYVE_RELAY_URL" -H "Authorization: Bearer $HYVE_RELAY_TOKEN" -H "X-Hyve-Status: ok" --data-binary @"$KUBECONFIG"
else
  curl -sf -X POST "$HYVE_RELAY_URL" -H "Authorization: Bearer $HYVE_RELAY_TOKEN" -H "X-Hyve-Status: error" --data-binary "auth operation exited $__hyve_rc"
fi
`, authScript)
}

// describeMintTimeout best-effort inspects the Job's pod for a clearer
// timeout message (e.g. an image pull failure) than a bare "timed out" —
// purely diagnostic, never a source of the kubeconfig itself.
func (s *Server) describeMintTimeout(ctx context.Context, namespace, jobName string) string {
	pods, err := s.Clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{LabelSelector: "job-name=" + jobName})
	if err != nil || len(pods.Items) == 0 {
		return "timed out waiting for access method job to report a result"
	}
	for _, cs := range pods.Items[0].Status.ContainerStatuses {
		if cs.State.Waiting != nil {
			return fmt.Sprintf("timed out waiting for access method job (container waiting: %s: %s)", cs.State.Waiting.Reason, cs.State.Waiting.Message)
		}
		if cs.State.Terminated != nil {
			return fmt.Sprintf("timed out waiting for access method job (container exited %d without reporting a result)", cs.State.Terminated.ExitCode)
		}
	}
	return "timed out waiting for access method job to report a result"
}

// defaultModuleImage best-effort reads HyveConfig.spec.defaultModuleImage
// as AccessMethodSpec.Runner.Image's second fallback tier (the first is
// Runner.Image itself; the third and last is k8sjob.PushJob's own
// hardcoded fallback when this also returns "") — mirrors the same
// two-tier convention ClusterDefinition's own module dispatch already
// uses via internal/reconcile.Manager, just read directly here since the
// API has no equivalent startup-cached copy of HyveConfig. Empty (not an
// error) on any failure, same "not configured yet is fine" stance
// s.githubToken already takes for a different best-effort Secret read.
func (s *Server) defaultModuleImage(ctx context.Context) string {
	var cfg hyvev1alpha1.HyveConfig
	if err := s.Client.Get(ctx, types.NamespacedName{Namespace: s.Namespace, Name: hyveConfigSingletonName}, &cfg); err != nil {
		return ""
	}
	return cfg.Spec.DefaultModuleImage
}

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
