package module

import (
	"fmt"
	"os/exec"
	"strings"
)

// ValidateToolRequirements checks that every required tool is present on
// PATH. Deliberately does not enforce ToolRequirement.Version — no module.yaml
// in the wild sets it yet, and duplicating workflow.RequirementValidator's
// semver-ish comparison logic for an unused field isn't worth the complexity.
// Version/Description are still surfaced in the error message for context.
func ValidateToolRequirements(tools []ToolRequirement) error {
	var missing []string
	for _, t := range tools {
		if _, err := exec.LookPath(t.Name); err != nil {
			msg := fmt.Sprintf("required tool '%s' not found in PATH", t.Name)
			if t.Description != "" {
				msg = fmt.Sprintf("%s (%s)", msg, t.Description)
			}
			missing = append(missing, msg)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return fmt.Errorf("module tool requirements not met:\n  - %s", strings.Join(missing, "\n  - "))
}

// ValidateEnvRequirements checks that every required env var in envReqs is
// present (non-empty) in env (the KEY=value slice a module's operation is
// about to run with — see internal/reconcile.buildModuleEnv). Confirmed
// live, this exact gap: a driver module whose create/status/... scripts
// need e.g. CIVO_TOKEN and it's simply missing from a cluster-mode
// install's hyve-cli-secrets doesn't fail loudly — the underlying CLI
// tool the script calls (civo, in that case) fails to authenticate in
// whatever way it fails, which may not even look like an error the
// script's own NOT_FOUND-detection recognizes, so hyve can end up
// treating an inconclusive/garbled status as "nothing to do" — a cluster
// silently never gets created, with the ClusterDefinition's own Ready
// condition still reporting true (see reconcileCluster's default-case
// "Unhandled status" branch, which returns nil, not an error). Catching a
// genuinely missing required env var before ever running the script turns
// that into one clear, immediate error instead.
//
// Deliberately NOT called for a local/CLI-mode run (see
// reconcileCluster's own call site) — a required env var there may have
// an equally valid non-env alternative hyve has no visibility into (this
// exact module's own CIVO_TOKEN requirement documents one: "Alternative:
// run `civo apikey save` before reconciling"), so enforcing presence
// there would produce false positives for an already-working local setup.
// A cluster-mode Job's container starts fresh every time with no such
// alternative — the env var (sourced from hyve-cli-secrets) is the only
// way the requirement can ever be satisfied there, which is what makes it
// safe to enforce as a hard precondition in that mode specifically.
func ValidateEnvRequirements(envReqs []EnvRequirement, env []string) error {
	var missing []string
	for _, e := range envReqs {
		if envValue(env, e.Name) != "" {
			continue
		}
		msg := fmt.Sprintf("required env var '%s' not set", e.Name)
		if e.Description != "" {
			msg = fmt.Sprintf("%s (%s)", msg, e.Description)
		}
		missing = append(missing, msg)
	}
	if len(missing) == 0 {
		return nil
	}
	return fmt.Errorf("module env requirements not met:\n  - %s", strings.Join(missing, "\n  - "))
}

// envValue returns key's value from a KEY=value slice (the shape
// internal/reconcile.buildModuleEnv produces), or "" if key isn't present
// — mirrors internal/reconcile's own unexported helper of the same name;
// duplicated rather than imported since internal/reconcile already
// imports this package, not the other way around.
func envValue(env []string, key string) string {
	prefix := key + "="
	for _, kv := range env {
		if strings.HasPrefix(kv, prefix) {
			return kv[len(prefix):]
		}
	}
	return ""
}
