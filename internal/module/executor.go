package module

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/cbridges1/hyve/internal/secretsfrom"
)

// DefaultKubeconfigPath returns the path kubectl uses when KUBECONFIG is unset.
func DefaultKubeconfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	return filepath.Join(home, ".kube", "config"), nil
}

// kubeconfigNameSanitizer replaces anything but [A-Za-z0-9_.-] so a cluster
// name can never escape the kubeconfigs directory or collide via path
// separators.
var kubeconfigNameSanitizer = regexp.MustCompile(`[^A-Za-z0-9_.-]+`)

// KubeconfigPathForCluster returns a kubeconfig file path unique to
// clusterName, under ~/.hyve/kubeconfigs/. Auth exports are written here
// instead of the shared ~/.kube/config so that two clusters' auth/reconcile
// operations can safely run concurrently (see MaxConcurrentReconciles) —
// each cluster's context lives in its own file, never a shared mutable one.
// Ensures the parent directory exists.
func KubeconfigPathForCluster(clusterName string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	dir := filepath.Join(home, ".hyve", "kubeconfigs")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", fmt.Errorf("create kubeconfigs directory: %w", err)
	}
	safe := kubeconfigNameSanitizer.ReplaceAllString(clusterName, "-")
	return filepath.Join(dir, safe+".yaml"), nil
}

// Executor runs module operations in host mode.
type Executor struct {
	ModuleDir string
	Env       []string // HYVE_* vars as "KEY=VALUE" strings
	WorkDir   string   // repo root
	// ClusterName identifies which cluster this Executor acts on — used
	// only to derive a per-cluster kubeconfig path in executeAuth (see
	// KubeconfigPathForCluster). Every OperationAuth caller must set it.
	ClusterName string
	AuthMethod  string // optional: name of auth method to use; empty means first

	// Runner, if non-nil, dispatches create/status/delete/auth operations to
	// a fresh Kubernetes Job (see JobRunner) instead of running them inline
	// via os/exec — cluster mode only. Local/CLI mode never sets this, so
	// its behavior is completely unchanged.
	Runner *JobRunner
	// Image is the container image Runner should use. Only consulted when
	// Runner != nil.
	Image string
}

// Execute runs a named operation and returns captured outputs.
func (e *Executor) Execute(ctx context.Context, op OperationType) (*OperationResult, error) {
	path, ok := FindOperationFile(e.ModuleDir, op)
	if !ok {
		// Operation not implemented — return empty result (e.g. scale.yaml missing is OK)
		return &OperationResult{Outputs: map[string]string{}, ExitCode: 0}, nil
	}
	if strings.HasSuffix(path, ".yaml") {
		return e.executeYAML(ctx, path)
	}
	return e.executeScript(ctx, path)
}

// FindOperationFile returns op's operation file path in moduleDir, checking
// <op>.yaml, <op>.sh, then <op> in that order — exported so a caller that
// needs to know an operation's file without executing it (e.g. internal/api's
// auth-context endpoint, which delivers auth.yaml's raw content to a CLI to
// run entirely client-side, without that CLI needing its own hyve.lock/
// module resolution at all) doesn't have to duplicate this precedence.
func FindOperationFile(moduleDir string, op OperationType) (path string, ok bool) {
	opStr := string(op)
	for _, candidate := range []string{opStr + ".yaml", opStr + ".sh", opStr} {
		p := filepath.Join(moduleDir, candidate)
		if fileExists(p) {
			return p, true
		}
	}
	return "", false
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func (e *Executor) executeYAML(ctx context.Context, path string) (*OperationResult, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}

	// Detect kind
	var meta struct {
		Kind string `yaml:"kind"`
	}
	if err := yaml.Unmarshal(data, &meta); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}

	switch meta.Kind {
	case "ClusterAuth":
		var ca ClusterAuth
		if err := yaml.Unmarshal(data, &ca); err != nil {
			return nil, fmt.Errorf("parse ClusterAuth %s: %w", path, err)
		}
		return e.executeAuth(ctx, ca.Spec, e.AuthMethod)

	case "Workflow":
		return e.executeWorkflow(ctx, data, filepath.Base(path))

	default:
		return nil, fmt.Errorf("unknown kind %q in %s", meta.Kind, path)
	}
}

// executeWorkflow runs a kind:Workflow YAML by concatenating each step's
// run/script/command into a single shell script. This keeps the module
// system decoupled from the workflow executor.
//
// spec.secretsFrom (see internal/secretsfrom and HYVE-IMPLEMENTATION-PLAN.md's
// Phase 5 "extend secretsFrom to module operations") is resolved before
// running the combined script, in this process either way — a live
// kubeconfig-backed fetch, unlike internal/workflow's runtime: client case
// which branches on StepRunner — regardless of whether the combined script
// itself then runs inline or is dispatched to a Job (see e.Runner below).
//
// name identifies the dispatched run when e.Runner != nil (see
// executeScriptViaJob's identical use of filepath.Base) — irrelevant to
// the inline path, which never sends anything through k8sjob.Run.
func (e *Executor) executeWorkflow(ctx context.Context, data []byte, name string) (*OperationResult, error) {
	type step struct {
		Run     string `yaml:"run"`
		Script  string `yaml:"script"`
		Command string `yaml:"command"`
	}
	type job struct {
		Steps []step `yaml:"steps"`
	}
	// Support both single-job (map[string]job) and list-of-jobs ([]WorkflowJob) forms.
	var asMap struct {
		Spec struct {
			Jobs        map[string]job             `yaml:"jobs"`
			SecretsFrom []secretsfrom.SecretSource `yaml:"secretsFrom"`
		} `yaml:"spec"`
	}
	var asList struct {
		Spec struct {
			Jobs        []job                      `yaml:"jobs"`
			SecretsFrom []secretsfrom.SecretSource `yaml:"secretsFrom"`
		} `yaml:"spec"`
	}

	var scripts []string
	var secretSources []secretsfrom.SecretSource
	if err := yaml.Unmarshal(data, &asMap); err == nil && len(asMap.Spec.Jobs) > 0 {
		for _, j := range asMap.Spec.Jobs {
			for _, s := range j.Steps {
				if script := pickScript(s.Run, s.Script, s.Command); script != "" {
					scripts = append(scripts, script)
				}
			}
		}
		secretSources = asMap.Spec.SecretsFrom
	} else if err := yaml.Unmarshal(data, &asList); err == nil && len(asList.Spec.Jobs) > 0 {
		for _, j := range asList.Spec.Jobs {
			for _, s := range j.Steps {
				if script := pickScript(s.Run, s.Script, s.Command); script != "" {
					scripts = append(scripts, script)
				}
			}
		}
		secretSources = asList.Spec.SecretsFrom
	}

	if len(scripts) == 0 {
		return &OperationResult{Outputs: map[string]string{}, ExitCode: 0}, nil
	}

	var secretEnv []string
	for _, src := range secretSources {
		resolved, err := secretsfrom.Resolve(ctx, KubeconfigPathForCluster, src)
		if err != nil {
			return nil, fmt.Errorf("resolve secretsFrom: %w", err)
		}
		for k, v := range resolved {
			secretEnv = append(secretEnv, k+"="+v)
		}
	}

	combined := "set -e\n" + strings.Join(scripts, "\n")

	if e.Runner != nil {
		stdout, exitCode, runErr := e.Runner.Run(ctx, name, e.Image, combined, append(append([]string{}, e.Env...), secretEnv...))
		if runErr != nil && exitCode == 0 {
			// Dispatch/infra-level failure, not the script itself exiting
			// non-zero — mirrors executeScriptViaJob's identical handling.
			return nil, fmt.Errorf("workflow %s: %w", name, runErr)
		}
		return &OperationResult{Outputs: parseOutputs([]byte(stdout)), ExitCode: exitCode}, nil
	}

	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", combined)
	cmd.Env = append(append(os.Environ(), e.Env...), secretEnv...)
	cmd.Dir = e.WorkDir
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()

	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		}
		return &OperationResult{Outputs: parseOutputs(out), ExitCode: exitCode}, err
	}
	return &OperationResult{Outputs: parseOutputs(out), ExitCode: 0}, nil
}

func pickScript(run, script, command string) string {
	switch {
	case run != "":
		return run
	case script != "":
		return script
	case command != "":
		return command
	}
	return ""
}

func (e *Executor) executeAuth(ctx context.Context, spec ClusterAuthSpec, methodName string) (*OperationResult, error) {
	methods := spec.Methods
	if len(methods) == 0 {
		// Wrap legacy single-method spec so the rest of the logic is uniform.
		methods = []AuthMethod{{Name: "default", Auth: spec.Bootstrap}}
	}

	method := &methods[0] // default: first in list
	if methodName != "" {
		method = findMethod(methods, methodName)
		if method == nil {
			return nil, fmt.Errorf("auth method %q not found", methodName)
		}
	}

	// Contract (unchanged from before Job dispatch existed, and identical
	// for every module author regardless of mode): a method that produces a
	// kubeconfig sets exports: KUBECONFIG (or KEEPER_KUBECONFIG) and its
	// script writes the file it finds at $KUBECONFIG — see
	// runAuthScript for how that plain contract is honored whether the
	// script actually runs inline (same process, same filesystem, no relay
	// needed) or dispatched to a separate, ephemeral Job pod (hyve wraps
	// the script itself to relay the file back over stdout — the module's
	// own script never sees or needs to know about that wrapping).
	wantsKubeconfig := method.Exports == "KUBECONFIG" || method.Exports == "KEEPER_KUBECONFIG"

	var kcPath string
	if wantsKubeconfig {
		path, pathErr := KubeconfigPathForCluster(e.ClusterName)
		if pathErr != nil {
			return nil, fmt.Errorf("resolve per-cluster kubeconfig path: %w", pathErr)
		}
		kcPath = path
	}

	if err := e.runAuthScript(ctx, "auth-"+method.Name, method.Auth.Script, wantsKubeconfig, kcPath); err != nil {
		return nil, fmt.Errorf("auth method %q failed: %w", method.Name, err)
	}

	outputs := map[string]string{}
	if kcPath != "" {
		if _, statErr := os.Stat(kcPath); statErr == nil {
			outputs["KUBECONFIG"] = kcPath
		}
	}

	// Legacy verify step — only applies when using the legacy single-method
	// shape, and always runs inline in this process regardless of mode: it
	// needs kcPath, which this call just wrote to *this process's* local
	// disk — a separately-dispatched Job's pod would have no access to it.
	if len(spec.Methods) == 0 && spec.Verify != nil && spec.Verify.Command != "" {
		var verifyEnv []string
		if kcPath != "" {
			verifyEnv = []string{"KUBECONFIG=" + kcPath}
		}
		if err := e.runShellScriptWithEnv(ctx, spec.Verify.Command, verifyEnv); err != nil {
			return nil, fmt.Errorf("auth verify failed: %w", err)
		}
	}

	return &OperationResult{Outputs: outputs, ExitCode: 0}, nil
}

// jobKubeconfigTempPath is where runAuthScript's wrapper points $KUBECONFIG
// inside a dispatched Job's own container — never seen or chosen by the
// module's script itself, which only ever knows "$KUBECONFIG points
// somewhere I should write to," exactly as it always has.
const jobKubeconfigTempPath = "/tmp/hyve-auth-kubeconfig"

const kubeconfigBeginMarker = "___HYVE_KUBECONFIG_BEGIN___"
const kubeconfigEndMarker = "___HYVE_KUBECONFIG_END___"

// runAuthScript runs an auth method's script unmodified from the module
// author's point of view: it writes a kubeconfig to $KUBECONFIG if
// wantsKubeconfig, exactly like every version of this contract before Job
// dispatch existed. Inline (e.Runner == nil), $KUBECONFIG is set directly
// to kcPath — same process, same filesystem, done. Dispatched to a Job
// (e.Runner != nil), a fresh ephemeral pod shares no filesystem with this
// process, so the script is wrapped (invisible to its author) to point
// $KUBECONFIG at a private in-container temp path and relay that file back
// over stdout, between sentinel markers, once the script itself succeeds —
// runAuthScript then extracts it and writes it to kcPath here. The
// script's own text is embedded in a subshell so an explicit `exit` inside
// it (a common pattern on an error path) only ends that subshell, not the
// wrapper — otherwise the relay logic after it would never run.
func (e *Executor) runAuthScript(ctx context.Context, name, script string, wantsKubeconfig bool, kcPath string) error {
	if e.Runner == nil {
		var env []string
		if wantsKubeconfig {
			env = []string{"KUBECONFIG=" + kcPath}
		}
		return e.runShellScriptWithEnv(ctx, script, env)
	}

	dispatched := script
	if wantsKubeconfig {
		dispatched = fmt.Sprintf(`export KUBECONFIG=%s
(
%s
)
__hyve_rc=$?
if [ $__hyve_rc -eq 0 ] && [ -f "$KUBECONFIG" ]; then
  echo "%s"
  cat "$KUBECONFIG"
  echo ""
  echo "%s"
fi
exit $__hyve_rc`, jobKubeconfigTempPath, script, kubeconfigBeginMarker, kubeconfigEndMarker)
	}

	stdout, _, err := e.Runner.Run(ctx, name, e.Image, dispatched, e.Env)
	if err != nil {
		return err
	}
	if wantsKubeconfig {
		if content, ok := extractBetweenMarkers(stdout, kubeconfigBeginMarker, kubeconfigEndMarker); ok {
			if writeErr := os.WriteFile(kcPath, []byte(content), 0600); writeErr != nil {
				return fmt.Errorf("write kubeconfig for cluster %q: %w", e.ClusterName, writeErr)
			}
		}
	}
	return nil
}

// extractBetweenMarkers recovers the exact original file bytes the wrapper
// in runAuthScript relayed. Byte accounting, not line splitting: the
// wrapper always emits `cat "$KUBECONFIG"` (the file's raw bytes,
// whatever they are) immediately followed by one unconditional blank
// `echo` (exactly one "\n", regardless of whether the file itself ended in
// a newline) before the end marker's own line — so the span between the
// begin marker's line and the "\n"+end substring is always exactly
// original_content + "\n", and slicing it off at the start of that "\n"
// recovers original_content byte-for-byte, whether or not the file had a
// trailing newline of its own. (A line-split-and-rejoin approach was tried
// first and got this wrong: it can't tell "file had a trailing newline"
// apart from "file didn't," since both produce an empty trailing element
// after splitting.)
func extractBetweenMarkers(stdout, begin, end string) (string, bool) {
	beginLine := begin + "\n"
	startIdx := strings.Index(stdout, beginLine)
	if startIdx < 0 {
		return "", false
	}
	contentStart := startIdx + len(beginLine)

	needle := "\n" + end
	relEndIdx := strings.Index(stdout[contentStart:], needle)
	if relEndIdx < 0 {
		return "", false
	}
	return stdout[contentStart : contentStart+relEndIdx], true
}

// findMethod returns the first method with the given name, or nil if not found.
func findMethod(methods []AuthMethod, name string) *AuthMethod {
	for i := range methods {
		if methods[i].Name == name {
			return &methods[i]
		}
	}
	return nil
}

func (e *Executor) executeScript(ctx context.Context, scriptPath string) (*OperationResult, error) {
	if e.Runner != nil {
		return e.executeScriptViaJob(ctx, scriptPath)
	}

	cmd := exec.CommandContext(ctx, "/bin/sh", scriptPath)
	cmd.Env = append(os.Environ(), e.Env...)
	cmd.Dir = e.WorkDir
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			return nil, fmt.Errorf("script %s: %w", scriptPath, err)
		}
	}
	return &OperationResult{
		Outputs:  parseOutputs(out),
		ExitCode: exitCode,
	}, nil
}

// executeScriptViaJob reads scriptPath's own content and dispatches it to a
// fresh Kubernetes Job via e.Runner — the Job's pod has no access to this
// process's filesystem, so the script's bytes (not its path) are what's
// actually sent to run.
func (e *Executor) executeScriptViaJob(ctx context.Context, scriptPath string) (*OperationResult, error) {
	content, err := os.ReadFile(scriptPath)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", scriptPath, err)
	}
	stdout, exitCode, runErr := e.Runner.Run(ctx, filepath.Base(scriptPath), e.Image, string(content), e.Env)
	if runErr != nil && exitCode == 0 {
		// Dispatch/infra-level failure (couldn't create or wait for the
		// Job) rather than the script itself exiting non-zero — that case
		// is reported via ExitCode below instead, mirroring the inline
		// os/exec path's *exec.ExitError handling above.
		return nil, fmt.Errorf("script %s: %w", scriptPath, runErr)
	}
	return &OperationResult{
		Outputs:  parseOutputs([]byte(stdout)),
		ExitCode: exitCode,
	}, nil
}

// runShellScriptWithEnv runs script inline via os/exec (never dispatched to
// a Job — see runAuthScript and executeAuth's verify step, its only two
// callers) with e.Env plus extraEnv layered on top of the inherited process
// environment — extraEnv wins on key collision (exec.Cmd resolves duplicate
// keys to the last occurrence), which is how the auth/verify paths override
// KUBECONFIG per-cluster without touching os.Environ.
func (e *Executor) runShellScriptWithEnv(ctx context.Context, script string, extraEnv []string) error {
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", script)
	cmd.Env = append(append(os.Environ(), e.Env...), extraEnv...)
	cmd.Dir = e.WorkDir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func parseOutputs(stdout []byte) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(string(stdout), "\n") {
		if idx := strings.IndexByte(line, '='); idx > 0 {
			key := strings.TrimSpace(line[:idx])
			if strings.HasPrefix(key, "HYVE_") {
				out[key] = line[idx+1:]
			}
		}
	}
	return out
}
