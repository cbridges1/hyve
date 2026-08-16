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
			Jobs map[string]job `yaml:"jobs"`
		} `yaml:"spec"`
	}
	var asList struct {
		Spec struct {
			Jobs []job `yaml:"jobs"`
		} `yaml:"spec"`
	}

	var scripts []string
	if err := yaml.Unmarshal(data, &asMap); err == nil && len(asMap.Spec.Jobs) > 0 {
		for _, j := range asMap.Spec.Jobs {
			for _, s := range j.Steps {
				if script := pickScript(s.Run, s.Script, s.Command); script != "" {
					scripts = append(scripts, script)
				}
			}
		}
	} else if err := yaml.Unmarshal(data, &asList); err == nil && len(asList.Spec.Jobs) > 0 {
		for _, j := range asList.Spec.Jobs {
			for _, s := range j.Steps {
				if script := pickScript(s.Run, s.Script, s.Command); script != "" {
					scripts = append(scripts, script)
				}
			}
		}
	}

	if len(scripts) == 0 {
		return &OperationResult{Outputs: map[string]string{}, ExitCode: 0}, nil
	}

	combined := "set -e\n" + strings.Join(scripts, "\n")
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", combined)
	cmd.Env = append(os.Environ(), e.Env...)
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

	// For an export-producing method, point KUBECONFIG at a path unique to
	// this cluster (not the shared ~/.kube/config) *before* running the auth
	// script, so tools like civo/eks/gcloud kubeconfig plugins — which all
	// honor KUBECONFIG for --save/--merge — write their context there
	// instead. This is what makes concurrent reconciles of different
	// clusters safe: each has its own kubeconfig file, never a shared
	// mutable one keyed only by "whichever context is current".
	var authEnv []string
	var kcPath string
	switch method.Exports {
	case "KUBECONFIG", "KEEPER_KUBECONFIG":
		path, pathErr := KubeconfigPathForCluster(e.ClusterName)
		if pathErr != nil {
			return nil, fmt.Errorf("resolve per-cluster kubeconfig path: %w", pathErr)
		}
		kcPath = path
		authEnv = []string{"KUBECONFIG=" + kcPath}
	}

	if err := e.runShellScriptWithEnv(ctx, method.Auth.Script, authEnv); err != nil {
		return nil, fmt.Errorf("auth method %q failed: %w", method.Name, err)
	}

	outputs := map[string]string{}
	if kcPath != "" {
		if _, statErr := os.Stat(kcPath); statErr == nil {
			outputs["KUBECONFIG"] = kcPath
		}
	}

	// Legacy verify step — only applies when using the legacy single-method shape.
	if len(spec.Methods) == 0 && spec.Verify != nil && spec.Verify.Command != "" {
		if err := e.runShellScriptWithEnv(ctx, spec.Verify.Command, authEnv); err != nil {
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

// runShellScriptWithEnv runs script with e.Env plus extraEnv layered on top
// of the inherited process environment — extraEnv wins on key collision
// (exec.Cmd resolves duplicate keys to the last occurrence), which is how
// executeAuth overrides KUBECONFIG per-cluster without touching os.Environ.
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
