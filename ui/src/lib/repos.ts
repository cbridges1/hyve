// Fleet GitRepos and the bundles Fleet makes from them. Git owns what's in
// a bundle: the console shows bundles wherever they're useful but edits
// only the GitRepo (a Deployment's page).
import type { Tone } from './format'
import type { Obj } from './kube'
import { LABELS } from './types'

export const REPO_LABEL = 'fleet.cattle.io/repo-name'

// The Fleet cluster the host cluster (local) registers itself as.
export const LOCAL_FLEET_CLUSTER = 'fleet-local/local'

export function isUserRepo(g: Obj): boolean {
  return !g.metadata.labels?.[LABELS.OWNER_KIND]
}

export function bundleState(b: Obj): string {
  return b.status?.display?.state || (b.status?.summary && b.status.summary.ready === b.status.summary.desiredReady ? 'Ready' : 'Pending')
}

// Fleet's message (its Ready condition's), without the wrapper it puts
// around a cluster's: "ErrApplied(1) [Cluster fleet-local/local: …]" or
// "NotReady(1) [Cluster fleet-local/local]; …".
export function bundleMessage(b: Obj): string {
  if (bundleState(b) === 'Ready') return ''
  const m: string = (b.status?.conditions || []).find((c: any) => c.type === 'Ready' && c.status === 'False')?.message || ''
  return m
    .replace(/^\w+\(\d+\) \[Cluster [^:\]]+\]; /, '')
    .replace(/^\w+\(\d+\) \[Cluster [^:\]]+: (.*)\]$/s, '$1')
    .trim()
}

// Fleet's bundle and bundle-deployment states as a pill's tone.
export function fleetTone(state: string): Tone {
  if (!state || state === 'Ready') return 'good'
  if (/^(Err|Error|Failed)/.test(state)) return 'bad'
  if (state === 'OutOfSync' || state === 'Modified' || state === 'NotReady') return 'busy'
  return 'idle'
}

// Fleet names a bundle REPO-PATH-DIR (fleet/arr in nexus-config is
// nexus-config-fleet-arr): the directory under the repo's path is what
// tells a repo's bundles apart.
export function shortBundleName(repo: Obj, bundle: Obj): string {
  const name = repo.metadata.name
  const prefixes = [...(repo.spec?.paths || []).map((p: string) => `${name}-${p.replace(/^\/+|\/+$/g, '').replace(/\//g, '-')}-`), `${name}-`]
  const n = bundle.metadata.name
  const p = prefixes.find((x) => n.startsWith(x) && n.length > x.length)
  return p ? n.slice(p.length) : n
}
