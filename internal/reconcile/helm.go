package reconcile

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"

	"github.com/cbridges1/hyve/internal/types"
)

// helmConfigHash is a content hash used to detect config drift for a Helm
// resource, the same role resourceref's SHA256 plays for a manifest
// resource. Deterministic regardless of Go's randomized map iteration order
// — Values keys are sorted before hashing, not left to a library's map
// marshaling behavior. Pure — table-testable.
func helmConfigHash(h *types.HelmSpec) string {
	var b strings.Builder
	fmt.Fprintf(&b, "chart=%s\n", h.Chart)
	fmt.Fprintf(&b, "repo=%s\n", h.Repo)
	fmt.Fprintf(&b, "version=%s\n", h.Version)
	fmt.Fprintf(&b, "namespace=%s\n", h.Namespace)
	keys := make([]string, 0, len(h.Values))
	for k := range h.Values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(&b, "values.%s=%s\n", k, h.Values[k])
	}
	sum := sha256.Sum256([]byte(b.String()))
	return fmt.Sprintf("%x", sum[:])
}

// helmChartArgs builds the chart/repo/version/namespace/--set argument tail
// shared by `helm template` and `helm upgrade --install`. Values keys are
// sorted for deterministic, reproducible invocations.
func helmChartArgs(h *types.HelmSpec) []string {
	args := []string{h.Chart}
	if h.Repo != "" {
		args = append(args, "--repo", h.Repo)
	}
	if h.Version != "" {
		args = append(args, "--version", h.Version)
	}
	if h.Namespace != "" {
		args = append(args, "-n", h.Namespace)
	}
	keys := make([]string, 0, len(h.Values))
	for k := range h.Values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		args = append(args, "--set", fmt.Sprintf("%s=%s", k, h.Values[k]))
	}
	return args
}

// helmRenderManifest runs `helm template <name> <chart> ...` — pure
// client-side rendering, no cluster mutation. The output is fed straight
// into kubectlDiff for live-drift checking, reusing the exact same
// diff mechanism built for raw manifest resources.
func helmRenderManifest(ctx context.Context, workDir, releaseName string, h *types.HelmSpec) ([]byte, error) {
	args := append([]string{"template", releaseName}, helmChartArgs(h)...)
	cmd := exec.CommandContext(ctx, "helm", args...)
	cmd.Dir = workDir
	cmd.Env = os.Environ()
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("helm template: %w: %s", err, stderr.String())
	}
	return stdout.Bytes(), nil
}

// helmUpgradeInstall runs `helm upgrade --install <name> <chart> ...`. Passes
// --create-namespace when a namespace is set — most charts (including some
// widely-used ones, e.g. the official Portainer chart) don't render their own
// Namespace object and simply assume it already exists, so without this flag
// the very first install into a not-yet-existing namespace fails outright.
func helmUpgradeInstall(ctx context.Context, workDir, releaseName string, h *types.HelmSpec) error {
	args := append([]string{"upgrade", "--install", releaseName}, helmChartArgs(h)...)
	if h.Namespace != "" {
		args = append(args, "--create-namespace")
	}
	cmd := exec.CommandContext(ctx, "helm", args...)
	cmd.Dir = workDir
	cmd.Env = os.Environ()
	out, err := cmd.CombinedOutput()
	if len(out) > 0 {
		fmt.Print(string(out))
	}
	if err != nil {
		return fmt.Errorf("helm upgrade --install: %w", err)
	}
	return nil
}

// helmUninstall runs `helm uninstall <name>`, treating "release: not found"
// as success — idempotent, mirroring kubectl delete --ignore-not-found's
// spirit for the raw-manifest path.
func helmUninstall(ctx context.Context, workDir, releaseName, namespace string) error {
	args := []string{"uninstall", releaseName}
	if namespace != "" {
		args = append(args, "-n", namespace)
	}
	cmd := exec.CommandContext(ctx, "helm", args...)
	cmd.Dir = workDir
	cmd.Env = os.Environ()
	out, err := cmd.CombinedOutput()
	outStr := string(out)
	if len(out) > 0 {
		fmt.Print(outStr)
	}
	if err != nil {
		if strings.Contains(outStr, "release: not found") {
			return nil
		}
		return fmt.Errorf("helm uninstall: %w", err)
	}
	return nil
}

// helmGetManifest runs `helm get manifest <name>` to retrieve the currently
// deployed rendered manifest after a successful install/upgrade, so
// parseManifestObjects (unchanged, fully reused) can build the Objects list.
func helmGetManifest(ctx context.Context, workDir, releaseName, namespace string) ([]byte, error) {
	args := []string{"get", "manifest", releaseName}
	if namespace != "" {
		args = append(args, "-n", namespace)
	}
	cmd := exec.CommandContext(ctx, "helm", args...)
	cmd.Dir = workDir
	cmd.Env = os.Environ()
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("helm get manifest: %w: %s", err, stderr.String())
	}
	return stdout.Bytes(), nil
}
