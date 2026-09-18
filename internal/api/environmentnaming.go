package api

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/cbridges1/hyve/internal/orgdb"
)

// hyveEnvironmentLabel is applied to every environment-scoped object
// (ClusterDefinition/Template/Workflow/Resource) created while its target
// namespace resolves to a real Organization — see resolveResourceEnvironment
// below. Never present on an object created before Milestone 3, or in a
// namespace with no matching Organization (notably the control-plane
// namespace itself, which is never an organization).
const hyveEnvironmentLabel = "hyve.io/environment"

// joinEnvironmentName computes the real Kubernetes metadata.name for a
// short, user-facing resource name within environment — see
// HYVE-ORGANIZATION-MODEL-PROPOSAL.md's "Naming collisions are avoided by
// decoupling the user-facing name from the real Kubernetes object name"
// section. One-directional by design: construct here, never reverse-parse
// a raw metadata.name back into (environment, name) without already
// knowing which environment you're asking about — see
// splitEnvironmentPrefix below for the only safe direction to go back.
func joinEnvironmentName(environment, shortName string) string {
	return environment + "-" + shortName
}

// splitEnvironmentPrefix recovers a short name from a real metadata.name,
// given the environment it's already known to belong to (e.g. read off the
// object's own hyveEnvironmentLabel) — safe specifically because the
// environment isn't being guessed from the name itself, unlike the
// "reverse-parse an arbitrary name" case joinEnvironmentName's own doc
// comment warns against.
func splitEnvironmentPrefix(realName, environment string) string {
	return strings.TrimPrefix(realName, environment+"-")
}

// envQueryParam is the query parameter every environment-scoped endpoint
// accepts to select which environment a request addresses — e.g.
// GET /clusters/{name}?env=staging. Omitted, resolveResourceEnvironment
// falls back to the organization's own single environment when it has
// exactly one (the common case — see HYVE-ORGANIZATION-MODEL-PROPOSAL.md's
// CLI/API surface section).
const envQueryParam = "env"

// resolveResourceEnvironment resolves which Environment (if any) a request
// touching one of the four environment-scoped resource types should be
// scoped to, given the resolved target namespace and the request's own
// ?env= query parameter.
//
// ok is false when namespace has no matching Organization in s.OrgStore at
// all — every namespace that predates Milestone 3, and the control-plane
// namespace itself (never an organization, see validateOrganizationName),
// behave exactly as they always have: no environment resolution, no label,
// no name-joining. This is what makes Milestone 3 additive rather than a
// breaking cutover for anything created before it landed — see
// HYVE-ORGANIZATION-MODEL-IMPLEMENTATION-PLAN.md's own "Sequencing
// principle".
//
// When ok is true and err is nil, env is always a real, resolved
// Environment belonging to that organization — never a zero value.
//
// A nil s.OrgStore (every Server built before Milestone 3's field existed,
// including every pre-existing test in this package) is treated exactly
// like "no organization for this namespace" — legacy behavior, not a
// panic. This is deliberate, not just defensive: it's what makes every
// handler wired to this function additive rather than a breaking change
// for a Server that hasn't opted into organizations at all.
func (s *Server) resolveResourceEnvironment(ctx context.Context, namespace, requestedEnv string) (env orgdb.Environment, ok bool, err error) {
	if s.OrgStore == nil {
		return orgdb.Environment{}, false, nil
	}
	org, err := s.OrgStore.GetOrganizationByName(ctx, namespace)
	if err == orgdb.ErrNotFound {
		return orgdb.Environment{}, false, nil
	}
	if err != nil {
		return orgdb.Environment{}, false, fmt.Errorf("resolve organization for namespace %q: %w", namespace, err)
	}

	if requestedEnv != "" {
		env, err := s.OrgStore.GetEnvironmentByName(ctx, org.ID, requestedEnv)
		if err == orgdb.ErrNotFound {
			return orgdb.Environment{}, true, fmt.Errorf("environment %q does not exist for organization %q", requestedEnv, org.Name)
		}
		if err != nil {
			return orgdb.Environment{}, true, fmt.Errorf("resolve environment %q: %w", requestedEnv, err)
		}
		return env, true, nil
	}

	envs, err := s.OrgStore.ListEnvironments(ctx, org.ID)
	if err != nil {
		return orgdb.Environment{}, true, fmt.Errorf("list environments for organization %q: %w", org.Name, err)
	}
	switch len(envs) {
	case 1:
		return envs[0], true, nil
	case 0:
		// Shouldn't happen in practice — every organization gets a default
		// environment at creation time — but a request against one that
		// somehow has none is a clearer 400 than a confusing empty-env
		// label downstream.
		return orgdb.Environment{}, true, fmt.Errorf("organization %q has no environments", org.Name)
	default:
		names := make([]string, len(envs))
		for i, e := range envs {
			names[i] = e.Name
		}
		return orgdb.Environment{}, true, fmt.Errorf("organization %q has multiple environments (%s) — specify ?env=", org.Name, strings.Join(names, ", "))
	}
}

// effectiveEnvironmentLabel backfills the display value for an object
// created before environments existed (or before its organization had
// more than one) — label itself, verbatim, when already set. Otherwise:
//
//   - exactly one environment: that one, unambiguously (the same
//     "no ambiguity, no explicit label needed" reasoning
//     resolveResourceEnvironment's own len(envs)==1 case already uses at
//     create time).
//   - two or more environments, one of them literally named
//     orgdb.DefaultEnvironmentName ("default"): that one specifically —
//     "default" is the one reserved, always-auto-created environment
//     (CreateOrganizationWithDefaults/ensureControlPlaneOrganization both
//     seed it, and it's the only environment name resolveResourceEnvironment
//     itself ever assumes), so every object that predates Milestone 3 — or
//     predates its organization's second environment — genuinely belongs
//     there, not to an arbitrarily later-created peer like "staging" or
//     "dev" it has no actual relationship to. Confirmed live on a
//     long-running control-plane organization that had since grown a second
//     environment ("dev") alongside "default": the len(envs)==1-only
//     version of this function left every pre-existing host-cluster
//     object unlabeled forever once that second environment existed,
//     which is the wrong call — "default" not being alone doesn't make it
//     any less the right home for something with no label at all.
//   - anything else (no Organization for namespace, zero environments, or
//     2+ environments none of which is named "default" — only reachable by
//     deleting the "default" environment specifically while another
//     remains, via DELETE /organizations/{name}/environments/{env}):
//     genuinely ambiguous, label returned unchanged (including "").
//
// Display-only: never writes the label back onto the object itself, so
// this has to be recomputed on every read, and a caller filtering
// client-side by environment needs to apply the identical fallback, not
// just this field, to see the same result a list endpoint already
// backfilled.
func (s *Server) effectiveEnvironmentLabel(ctx context.Context, namespace, label string) string {
	if label != "" || s.OrgStore == nil {
		return label
	}
	org, err := s.OrgStore.GetOrganizationByName(ctx, namespace)
	if err != nil {
		return label
	}
	envs, err := s.OrgStore.ListEnvironments(ctx, org.ID)
	if err != nil {
		return label
	}
	if len(envs) == 1 {
		return envs[0].Name
	}
	for _, env := range envs {
		if env.Name == orgdb.DefaultEnvironmentName {
			return env.Name
		}
	}
	return label
}

// resourceEnvironmentResult bundles what every environment-scoped resource
// handler needs after resolving a short name against its target namespace:
// the real Kubernetes metadata.name to actually use, and the label value
// (empty when the namespace isn't a real organization) to apply/match on.
type resourceEnvironmentResult struct {
	RealName       string
	Label          string // "" means: no organization for this namespace, legacy behavior
	HasEnvironment bool
}

// resolveCreateName is resolveResourceEnvironment plus the create-time
// naming join, in one call — the shape every handleCreate<Type> needs. On
// error, the caller should write it as a 400 Bad Request (an unresolvable
// ?env= or an ambiguous default is a client error, not a server one) and
// return without creating anything.
func (s *Server) resolveCreateName(w http.ResponseWriter, r *http.Request, namespace, shortName string) (resourceEnvironmentResult, bool) {
	env, ok, err := s.resolveResourceEnvironment(r.Context(), namespace, r.URL.Query().Get(envQueryParam))
	if err != nil {
		status := http.StatusBadRequest
		if !ok {
			status = http.StatusInternalServerError
		}
		writeError(w, status, err.Error())
		return resourceEnvironmentResult{}, false
	}
	if !ok {
		return resourceEnvironmentResult{RealName: shortName}, true
	}
	return resourceEnvironmentResult{RealName: joinEnvironmentName(env.Name, shortName), Label: env.Name, HasEnvironment: true}, true
}

// resolveAddressedName resolves a GET/PATCH/DELETE's {name} path value plus
// its ?env= query parameter into the real Kubernetes metadata.name to
// address — the read-side counterpart to resolveCreateName. Unlike create,
// a missing/ambiguous ?env= here degrades gracefully to treating {name} as
// the literal metadata.name (legacy behavior) rather than erroring, since a
// GET by exact name has always been valid regardless of environment and
// should stay that way for any pre-Milestone-3 caller.
func (s *Server) resolveAddressedName(r *http.Request, namespace, name string) string {
	requestedEnv := r.URL.Query().Get(envQueryParam)
	if requestedEnv == "" || s.OrgStore == nil {
		return name
	}
	org, err := s.OrgStore.GetOrganizationByName(r.Context(), namespace)
	if err != nil {
		return name
	}
	if _, err := s.OrgStore.GetEnvironmentByName(r.Context(), org.ID, requestedEnv); err != nil {
		return name
	}
	return joinEnvironmentName(requestedEnv, name)
}
