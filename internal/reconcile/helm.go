package reconcile

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"

	"github.com/cbridges1/hyve/internal/types"
)

// helmValueEnvVarPattern matches ${VAR_NAME} references in a Helm value
// string — braced form only (not bare $VAR), so a value that legitimately
// contains a literal '$' (a password, say) isn't misinterpreted.
var helmValueEnvVarPattern = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// resolveHelmValues expands ${VAR} references in h.Values against hyve's own
// process environment (populated from a local .env via godotenv.Load in
// main.go, or from a CI job's env: block — see reconcile.yml) so a value
// like a Pangolin org ID or API base URL can live in .env / GitHub Secrets
// instead of committed literally in a template that gets reused across
// ephemeral clusters. A referenced but unset variable is a hard error naming
// every missing one — the same fail-loud convention resolveSecretKeys uses,
// and for the same reason: silently substituting an empty string here would
// quietly re-disable whatever the value was configuring rather than
// surfacing the missing prerequisite.
func resolveHelmValues(h *types.HelmSpec) (map[string]string, error) {
	resolved := make(map[string]string, len(h.Values))
	missing := map[string]bool{}
	for k, v := range h.Values {
		resolved[k] = helmValueEnvVarPattern.ReplaceAllStringFunc(v, func(match string) string {
			name := match[2 : len(match)-1] // strip "${" and "}"
			val, ok := os.LookupEnv(name)
			if !ok {
				missing[name] = true
				return match
			}
			return val
		})
	}
	if len(missing) > 0 {
		names := make([]string, 0, len(missing))
		for name := range missing {
			names = append(names, name)
		}
		sort.Strings(names)
		return nil, fmt.Errorf("helm resource: missing required environment variable(s): %s", strings.Join(names, ", "))
	}
	return resolved, nil
}

// helmConfigHash is a content hash used to detect config drift for a Helm
// resource, the same role resourceref's SHA256 plays for a manifest
// resource. Takes the already-resolved values map (not h.Values) so a
// changed environment *value* is drift too, not just a changed values:
// map — the same reasoning secretConfigHash follows for Secret resources.
// Deterministic regardless of Go's randomized map iteration order — keys
// are sorted before hashing, not left to a library's map marshaling
// behavior. Pure — table-testable.
func helmConfigHash(h *types.HelmSpec, resolvedValues map[string]string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "chart=%s\n", h.Chart)
	fmt.Fprintf(&b, "repo=%s\n", h.Repo)
	fmt.Fprintf(&b, "version=%s\n", h.Version)
	fmt.Fprintf(&b, "namespace=%s\n", h.Namespace)
	keys := make([]string, 0, len(resolvedValues))
	for k := range resolvedValues {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(&b, "values.%s=%s\n", k, resolvedValues[k])
	}
	sum := sha256.Sum256([]byte(b.String()))
	return fmt.Sprintf("%x", sum[:])
}

// helmChartArgs builds the chart/repo/version/namespace/--set argument tail
// shared by `helm template` and `helm upgrade --install`, from the
// already-resolved values map. Values keys are sorted for deterministic,
// reproducible invocations.
func helmChartArgs(h *types.HelmSpec, resolvedValues map[string]string) []string {
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
	keys := make([]string, 0, len(resolvedValues))
	for k := range resolvedValues {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		args = append(args, "--set", fmt.Sprintf("%s=%s", k, resolvedValues[k]))
	}
	return args
}

// helmRenderManifest runs `helm template <name> <chart> ...` — pure
// client-side rendering, no cluster mutation. The output is fed straight
// into kubectlDiff for live-drift checking, reusing the exact same
// diff mechanism built for raw manifest resources.
func helmRenderManifest(ctx context.Context, workDir, releaseName string, h *types.HelmSpec, resolvedValues map[string]string) ([]byte, error) {
	args := append([]string{"template", releaseName}, helmChartArgs(h, resolvedValues)...)
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
func helmUpgradeInstall(ctx context.Context, workDir, releaseName string, h *types.HelmSpec, resolvedValues map[string]string) error {
	args := append([]string{"upgrade", "--install", releaseName}, helmChartArgs(h, resolvedValues)...)
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
