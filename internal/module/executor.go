package module

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Executor runs module operations in host mode.
type Executor struct {
	ModuleDir string
	Env       []string // HYVE_* vars as "KEY=VALUE" strings
	WorkDir   string   // repo root
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
		return e.executeAuth(ctx, ca.Spec)

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

func (e *Executor) executeAuth(ctx context.Context, spec ClusterAuthSpec) (*OperationResult, error) {
	if err := e.runShellScript(ctx, spec.Bootstrap.Script); err != nil {
		return nil, fmt.Errorf("auth bootstrap failed: %w", err)
	}
	if spec.Verify != nil && spec.Verify.Command != "" {
		if err := e.runShellScript(ctx, spec.Verify.Command); err != nil {
			return nil, fmt.Errorf("auth verify failed: %w", err)
		}
	}
	return &OperationResult{Outputs: map[string]string{}, ExitCode: 0}, nil
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

func (e *Executor) runShellScript(ctx context.Context, script string) error {
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", script)
	cmd.Env = append(os.Environ(), e.Env...)
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
