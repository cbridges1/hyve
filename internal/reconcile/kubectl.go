package reconcile

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/cbridges1/hyve/internal/types"
)

// kubectlApply runs `kubectl apply --server-side --force-conflicts -f -`,
// piping data via stdin rather than writing a temp file (resourceref.Resolve
// already returns the manifest as []byte in memory). --force-conflicts is
// required because spec.resources are hyve's declared desired state — a
// resource can legitimately need to claim a field another field manager
// already owns (e.g. resource-files/newt/blueprint-configmap.yaml
// intentionally overwrites the "helm" field manager's placeholder rendering
// of the same ConfigMap's .data.blueprint.yaml). Without it, kubectl refuses
// with a conflict error and the resource silently never reconciles. Not
// shared with internal/workflow/job_runner.go's kubectl-apply action, which
// still writes to a file path — out of scope for this change.
func kubectlApply(ctx context.Context, workDir string, data []byte, namespace string) error {
	args := []string{"apply", "--server-side", "--force-conflicts", "-f", "-"}
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
		// kubectl's dry-run diff fetches the object twice internally and warns
		// "keeps changing, diffing without lock" (exit 2, not the documented 0/1)
		// when something else mutates it between those fetches — e.g. a Helm
		// release and a raw resource both owning the same object, as with
		// newt-newt-main-tunnel-blueprint (see resource-files/newt/blueprint-
		// configmap.yaml). Treating this as a hard error aborts the whole
		// resource-reconcile loop and permanently wedges that resource — an
		// object that "keeps changing" is by definition out of sync, so treat
		// it the same as a real diff and let the caller re-apply instead.
		if strings.Contains(combined.String(), "keeps changing, diffing without lock") {
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
