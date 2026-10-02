package workflow

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"syscall"
)

// StepRunner abstracts how a workflow step's command/script actually
// executes — the same shape as internal/reconcile.StateProvider's
// extraction. env is the fully-layered environment (process env +
// workflow/job/step-level vars, already expanded) and workingDir is
// already resolved — callers (Executor.executeStep) do that layering once,
// up front, so every StepRunner implementation gets identical inputs
// regardless of where the step actually runs.
//
// output, when non-nil, additionally receives everything written to
// stdout/stderr while the step runs — mirrors Executor.Output, which
// Executor.executeStep already passed straight into exec.Cmd.Stdout/Stderr
// via a MultiWriter before this interface existed; a step runner that
// can't meaningfully stream live (e.g. one fetching a completed Job's pod
// logs after the fact) may write to it in one shot at the end instead.
//
// stdout and stderr are returned as the runner's best available separation
// of the two streams — LocalStepRunner and KubernetesJobStepRunner both
// only get a combined stream in practice (today's exec.Cmd.Stdout/Stderr
// already write into the same buffer; a Job's pod logs are equally
// combined by default), so both currently return the full combined output
// as stdout and leave stderr empty — documented here once rather than
// repeated on both implementations.
type StepRunner interface {
	// RequiresContainer reports whether this runner needs a resolved
	// container image to do anything at all. Gates container: pre-flight
	// validation in Executor.executeStep — "which StepRunner is selected,"
	// not "which mode hyve is in," is the actual condition that matters
	// (see HYVE-CONTROLLER-ARCHITECTURE-PLAN.md's Phase 4 "Workflow step
	// execution needs an explicit container" section): LocalStepRunner
	// returns false unconditionally (container: is meaningless — and
	// ignored, not validated — for a local subprocess), KubernetesJobStepRunner
	// returns true.
	RequiresContainer() bool

	RunStep(ctx context.Context, step WorkflowStep, env []string, workingDir string, output io.Writer) (stdout, stderr string, exitCode int, err error)
}

// LocalStepRunner runs a step as a local subprocess via exec.CommandContext
// — today's exact behavior (getShellCommand's sh -c/cmd /C dispatch,
// combined stdout+stderr capture, *exec.ExitError exit-code extraction),
// extracted as-is rather than reimplemented. The zero value is ready to
// use; this is Executor's default StepRunner, so every existing local-mode
// call site (hyve workflow run, hyve reconcile's lifecycle hooks) behaves
// identically to before this type existed.
type LocalStepRunner struct{}

// RequiresContainer always returns false — see StepRunner's doc comment.
func (LocalStepRunner) RequiresContainer() bool { return false }

// RunStep executes step.Command or step.Script (step.Action is handled
// entirely separately, before a StepRunner is ever consulted — see
// Executor.executeStep) as a local subprocess.
func (LocalStepRunner) RunStep(ctx context.Context, step WorkflowStep, env []string, workingDir string, output io.Writer) (stdout, stderr string, exitCode int, err error) {
	var command string
	var args []string

	switch {
	case step.Command != "":
		shellCmd, shellFlag := getShellCommand()
		command, args = shellCmd, []string{shellFlag, step.Command}
	case step.Script != "":
		shellCmd, shellFlag := getShellCommand()
		command, args = shellCmd, []string{shellFlag, step.Script}
	default:
		return "", "", 0, fmt.Errorf("no command or script specified")
	}

	cmd := exec.CommandContext(ctx, command, args...)
	cmd.Dir = workingDir
	cmd.Env = env

	// Stream live (so interactive steps, e.g. OAuth Device Authorization,
	// can print prompts before waiting for input) while also capturing the
	// full combined output into buf — matches executeStep's pre-extraction
	// behavior exactly.
	var buf bytes.Buffer
	stdoutWriters := []io.Writer{os.Stdout, &buf}
	stderrWriters := []io.Writer{os.Stderr, &buf}
	if output != nil {
		stdoutWriters = append(stdoutWriters, output)
		stderrWriters = append(stderrWriters, output)
	}
	cmd.Stdout = io.MultiWriter(stdoutWriters...)
	cmd.Stderr = io.MultiWriter(stderrWriters...)

	runErr := cmd.Run()
	combined := buf.String()

	code := 0
	if runErr != nil {
		if exitErr, ok := runErr.(*exec.ExitError); ok {
			if status, ok := exitErr.Sys().(syscall.WaitStatus); ok {
				code = status.ExitStatus()
			}
		}
	}
	return combined, "", code, runErr
}
