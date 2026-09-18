import { useEffect } from 'react'
import { organizationsApi } from '../lib/api/organizations'
import { useApi } from '../lib/useApi'
import { useWhoami } from '../lib/useWhoami'

// Shared by every environment-scoped resource-list page (Clusters,
// Templates, Workflows, Resources) — internal/api/environmentnaming.go's
// own ?env= query param is the only way any of those four resource types'
// create/list/get/patch/delete calls ever distinguish one environment
// from another; before this, the web console had no UI for it at all
// (confirmed live: once a second environment exists, every create request
// from this console 400s with an "ambiguous, specify ?env=" error, since
// nothing here ever sent one — see resolveResourceEnvironment's own
// `default:` case).
//
// useEnvironments resolves orgName the same way EnvironmentsPage already
// does (who.namespace doubles as the organization name — see that page's
// own doc comment) when orgName isn't passed explicitly, so most call
// sites can just call this with no arguments.
export function useEnvironments(orgName?: string) {
  const who = useWhoami().data
  const resolvedOrgName = orgName ?? who?.namespace
  return useApi(() => (resolvedOrgName ? organizationsApi.listEnvironments(resolvedOrgName) : Promise.resolve([])), [
    resolvedOrgName,
  ])
}

const selectClass =
  'rounded-lg border border-neutral-300 px-2.5 py-1.5 text-sm dark:border-neutral-700 dark:bg-neutral-800'

// EnvironmentFilterSelect lets a list page narrow an already-fetched list
// down to one environment — filtering happens client-side (list endpoints
// don't accept ?env= themselves, only create/get/patch/delete do), so this
// is deliberately just a plain controlled <select>, not something that
// re-fetches on change. Renders nothing when there's zero or one
// environment (the common case, "default" only) — nothing to switch
// between yet, so a dropdown offering just "All environments" and
// "default" would be pure noise.
//
// value/onChange are normally backed by useEnvironmentFilter (see that
// hook's own doc comment) rather than page-local state, so a selection
// here survives navigating to another resource-list page. That sharing is
// exactly why this component also self-corrects a stale value: switching
// organizations (or organizations, via the switcher) can leave value
// naming an environment the newly loaded org doesn't have, which would
// otherwise silently filter every row out with no visible cause.
export function EnvironmentFilterSelect({
  orgName,
  value,
  onChange,
}: {
  orgName?: string
  value: string
  onChange: (value: string) => void
}) {
  const { data: environments } = useEnvironments(orgName)

  useEffect(() => {
    // environments.length > 0, not just environments truthy: useEnvironments
    // resolves to [] both when an organization genuinely has zero
    // environments (never happens in practice — every organization gets a
    // "default" at creation) and, transiently, whenever orgName hasn't
    // resolved yet (e.g. right after navigating to another resource-list
    // page, before useWhoami's own per-mount refetch completes — see its
    // own doc comment on why that's a real network round trip, not
    // instant). Reacting to that second, transient case clobbered a just
    // set, still-valid filter on every navigation — confirmed live: dev
    // stayed selected on the page it was set on, then silently reset back
    // to "All environments" a moment after navigating to another page, as
    // soon as this effect saw that page's own brief real-environments=[]
    // window. Waiting for a real (non-empty) list before ever correcting
    // avoids treating "don't know yet" the same as "confirmed gone".
    if (value && environments && environments.length > 0 && !environments.some((env) => env.name === value)) {
      onChange('')
    }
  }, [value, environments, onChange])

  if (!environments || environments.length <= 1) return null

  return (
    <select value={value} onChange={(e) => onChange(e.target.value)} className={selectClass} aria-label="Filter by environment">
      <option value="">All environments</option>
      {environments.map((env) => (
        <option key={env.name} value={env.name}>
          {env.name}
        </option>
      ))}
    </select>
  )
}

// EnvironmentPickerField is the creation-form counterpart — picks which
// environment a new cluster/template/workflow/resource lands in (sent as
// ?env= by the caller's own submit handler, see e.g. ClustersListPage's
// NewClusterForm). Hidden under the same <=1 condition as the filter
// above, for the same reason plus a sharper one: resolveResourceEnvironment
// already auto-resolves a lone environment with no ?env= needed at all, so
// forcing a choice there would be redundant, not just noisy. Once a second
// environment exists, this becomes the only thing standing between a
// caller and that "ambiguous" 400 — defaultValue lets the calling page
// seed it from whatever its own list filter is currently set to, so
// creating from a filtered view lands in the environment you were just
// looking at, not a fresh choice every time.
export function EnvironmentPickerField({
  orgName,
  value,
  onChange,
  defaultValue,
}: {
  orgName?: string
  value: string
  onChange: (value: string) => void
  defaultValue?: string
}) {
  const { data: environments } = useEnvironments(orgName)

  if (!environments || environments.length <= 1) return null

  return (
    <label className="text-sm">
      <span className="mb-1 block text-neutral-600 dark:text-neutral-400">Environment</span>
      <select
        value={value || defaultValue || ''}
        onChange={(e) => onChange(e.target.value)}
        required
        className={`w-full ${selectClass}`}
      >
        <option value="" disabled>
          — select —
        </option>
        {environments.map((env) => (
          <option key={env.name} value={env.name}>
            {env.name}
          </option>
        ))}
      </select>
    </label>
  )
}

// EnvironmentBadge marks which environment a row belongs to — renders
// nothing when unset (every object created before environments existed,
// or in a namespace with no matching Organization at all).
export function EnvironmentBadge({ environment }: { environment?: string }) {
  if (!environment) return null
  return (
    <span className="rounded bg-sky-100 px-2 py-0.5 text-xs font-medium text-sky-800 dark:bg-sky-950 dark:text-sky-300">
      {environment}
    </span>
  )
}
