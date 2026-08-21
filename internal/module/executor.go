package module

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
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
	opStr := string(op)

	// Search for operation file in priority order: <op>.yaml, <op>.sh, <op>
	yamlPath := filepath.Join(e.ModuleDir, opStr+".yaml")
	shPath := filepath.Join(e.ModuleDir, opStr+".sh")
	binPath := filepath.Join(e.ModuleDir, opStr)

	switch {
	case fileExists(yamlPath):
		return e.executeYAML(ctx, yamlPath)
	case fileExists(shPath):
		return e.executeScript(ctx, shPath)
	case fileExists(binPath):
		return e.executeScript(ctx, binPath)
	default:
		// Operation not implemented — return empty result (e.g. scale.yaml missing is OK)
		return &OperationResult{Outputs: map[string]string{}, ExitCode: 0}, nil
	}
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
		return e.executeWorkflow(ctx, data)

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
// running the combined script — module operations always execute as a
// direct subprocess of whichever process (CLI or controller) is running the
// reconcile, never as a scheduled Kubernetes Job the way a workflow step
// under KubernetesJobStepRunner can, so there is only ever this one
// resolution path here (a live kubeconfig-backed fetch), unlike
// internal/workflow's runtime: client case which branches on StepRunner.
func (e *Executor) executeWorkflow(ctx context.Context, data []byte) (*OperationResult, error) {
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

	// Contract: an auth script that produces a kubeconfig prints
	// HYVE_KUBECONFIG_B64=<base64 kubeconfig content> to stdout — it's
	// responsible for generating that content itself (e.g. invoking a
	// provider's kubeconfig plugin against its own temp file and
	// base64-encoding it), since a Job-dispatched script (e.Runner != nil)
	// runs in a separate, ephemeral pod with no access to this process's
	// filesystem to write a shared path into. Applies identically whether
	// the script actually ran inline or as a Job — one contract, regardless
	// of invocation mode.
	stdout, err := e.runShellScriptWithEnv(ctx, "auth-"+method.Name, method.Auth.Script, nil)
	if err != nil {
		return nil, fmt.Errorf("auth method %q failed: %w", method.Name, err)
	}

	outputs := map[string]string{}
	var kcPath string
	if b64 := parseOutputs([]byte(stdout))["HYVE_KUBECONFIG_B64"]; b64 != "" {
		content, decErr := base64.StdEncoding.DecodeString(strings.TrimSpace(b64))
		if decErr != nil {
			return nil, fmt.Errorf("auth method %q: decode HYVE_KUBECONFIG_B64: %w", method.Name, decErr)
		}
		path, pathErr := KubeconfigPathForCluster(e.ClusterName)
		if pathErr != nil {
			return nil, fmt.Errorf("resolve per-cluster kubeconfig path: %w", pathErr)
		}
		if writeErr := os.WriteFile(path, content, 0600); writeErr != nil {
			return nil, fmt.Errorf("write kubeconfig for cluster %q: %w", e.ClusterName, writeErr)
		}
		kcPath = path
		outputs["KUBECONFIG"] = kcPath
	}

	// Legacy verify step — only applies when using the legacy single-method shape.
	if len(spec.Methods) == 0 && spec.Verify != nil && spec.Verify.Command != "" {
		var verifyEnv []string
		if kcPath != "" {
			verifyEnv = []string{"KUBECONFIG=" + kcPath}
		}
		if _, err := e.runShellScriptWithEnv(ctx, "auth-verify", spec.Verify.Command, verifyEnv); err != nil {
			return nil, fmt.Errorf("auth verify failed: %w", err)
		}
	}

	return &OperationResult{Outputs: outputs, ExitCode: 0}, nil
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

// runShellScriptWithEnv runs script — inline via os/exec, or dispatched to a
// fresh Job when e.Runner != nil — with e.Env plus extraEnv layered on top,
// and returns its captured stdout so callers (executeAuth) can read back
// the HYVE_KUBECONFIG_B64= contract regardless of where the script actually
// ran. extraEnv wins on key collision with e.Env (exec.Cmd resolves
// duplicate keys to the last occurrence).
func (e *Executor) runShellScriptWithEnv(ctx context.Context, name, script string, extraEnv []string) (string, error) {
	if e.Runner != nil {
		stdout, _, err := e.Runner.Run(ctx, name, e.Image, script, append(append([]string{}, e.Env...), extraEnv...))
		return stdout, err
	}

	var buf bytes.Buffer
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", script)
	cmd.Env = append(append(os.Environ(), e.Env...), extraEnv...)
	cmd.Dir = e.WorkDir
	cmd.Stdout = io.MultiWriter(&buf, os.Stdout)
	cmd.Stderr = os.Stderr
	err := cmd.Run()
	return buf.String(), err
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
