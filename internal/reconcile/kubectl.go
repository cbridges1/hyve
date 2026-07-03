package reconcile

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"

	"github.com/cbridges1/hyve/internal/types"
)

// kubectlApply runs `kubectl apply --server-side -f -`, piping data via
// stdin rather than writing a temp file (resourceref.Resolve already returns
// the manifest as []byte in memory). Not shared with
// internal/workflow/job_runner.go's kubectl-apply action, which still writes
// to a file path — out of scope for this change.
func kubectlApply(ctx context.Context, workDir string, data []byte, namespace string) error {
	args := []string{"apply", "--server-side", "-f", "-"}
	if namespace != "" {
		args = append(args, "-n", namespace)
	}
	cmd := exec.CommandContext(ctx, "kubectl", args...)
	cmd.Dir = workDir
	cmd.Env = os.Environ()
	cmd.Stdin = bytes.NewReader(data)
	out, err := cmd.CombinedOutput()
	if len(out) > 0 {
		fmt.Print(string(out))
	}
	if err != nil {
		return fmt.Errorf("kubectl apply: %w", err)
	}
	return nil
}

// kubectlDiff runs `kubectl diff --server-side -f -`. Exit code 0 means no
// diff; exit code 1 means a diff was found (not an error — kubectl diff's
// documented exit-code convention); any other exit code (or a non-ExitError
// failure, e.g. kubectl not found) is a real error.
func kubectlDiff(ctx context.Context, workDir string, data []byte, namespace string) (hasDiff bool, err error) {
	args := []string{"diff", "--server-side", "-f", "-"}
	if namespace != "" {
		args = append(args, "-n", namespace)
	}
	cmd := exec.CommandContext(ctx, "kubectl", args...)
	cmd.Dir = workDir
	cmd.Env = os.Environ()
	cmd.Stdin = bytes.NewReader(data)
	var combined bytes.Buffer
	cmd.Stdout = &combined
	cmd.Stderr = &combined

	runErr := cmd.Run()
	if runErr == nil {
		return false, nil
	}
	var exitErr *exec.ExitError
	if errors.As(runErr, &exitErr) {
		if exitErr.ExitCode() == 1 {
			return true, nil
		}
		return false, fmt.Errorf("kubectl diff: exit %d: %s", exitErr.ExitCode(), combined.String())
	}
	return false, fmt.Errorf("kubectl diff: %w", runErr)
}

// kubectlDeleteObjects deletes each tracked object by identity
// (apiVersion/kind/namespace/name), not by re-applying the original
// manifest (which may no longer exist locally, e.g. after a source file was
// deleted alongside delete:true). --ignore-not-found so an object someone
// already hand-deleted doesn't fail the whole cycle.
func kubectlDeleteObjects(ctx context.Context, workDir string, objects []types.AppliedObject) error {
	for _, obj := range objects {
		args := []string{"delete", obj.Kind, obj.Name, "--ignore-not-found"}
		if obj.Namespace != "" {
			args = append(args, "-n", obj.Namespace)
		}
		cmd := exec.CommandContext(ctx, "kubectl", args...)
		cmd.Dir = workDir
		cmd.Env = os.Environ()
		out, err := cmd.CombinedOutput()
		if len(out) > 0 {
			fmt.Print(string(out))
		}
		if err != nil {
			return fmt.Errorf("kubectl delete %s/%s: %w", obj.Kind, obj.Name, err)
		}
	}
	return nil
}
