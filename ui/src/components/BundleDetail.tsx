// One bundle from a git repo, opened: why it fails (the kinds the cluster
// doesn't serve, Fleet's message), then each object it deployed with its
// state, from its BundleDeployments (one per target cluster; a cluster's
// page passes just its own). Read-only: git owns the bundle. An object on
// the host opens as YAML; on an environment's cluster, in its explorer.
import { Link, useLocation } from 'react-router-dom'
import type { Tone } from '../lib/format'
import { typeForKind, type Obj } from '../lib/kube'
import { bundleMessage, fleetTone, LOCAL_FLEET_CLUSTER } from '../lib/repos'
import { FLEET } from '../lib/resources'
import { backFrom, paths } from '../lib/routes'
import { CodeBlock, Pill } from './ui'

type Res = { apiVersion: string; kind: string; namespace?: string; name: string; state: string; message: string; clusters: string[] }

// The Fleet cluster ("namespace/name") a BundleDeployment is for.
const bdCluster = (bd: Obj) => {
  const l = bd.metadata.labels || {}
  return l['fleet.cattle.io/cluster'] ? `${l['fleet.cattle.io/cluster-namespace'] || bd.metadata.namespace}/${l['fleet.cattle.io/cluster']}` : bd.metadata.namespace || ''
}

// What Fleet applied, with what isn't ready or was changed.
function resourcesOf(bds: Obj[]): Res[] {
  const out = new Map<string, Res>()
  const key = (r: any) => `${r.apiVersion}/${r.kind}/${r.namespace || ''}/${r.name}`
  for (const bd of bds) {
    const cluster = bdCluster(bd)
    for (const r of bd.status?.resources || []) {
      const k = key(r)
      const e: Res = out.get(k) || { apiVersion: r.apiVersion, kind: r.kind, namespace: r.namespace, name: r.name, state: 'Ready', message: '', clusters: [] }
      e.clusters.push(cluster)
      out.set(k, e)
    }
    for (const r of bd.status?.nonReadyStatus || []) {
      const e = out.get(key(r))
      if (!e) continue
      e.state = r.summary?.state || 'NotReady'
      e.message = (r.summary?.message || []).join('; ')
    }
    for (const r of bd.status?.modifiedStatus || []) {
      const k = key(r)
      const e = out.get(k) || { apiVersion: r.apiVersion, kind: r.kind, namespace: r.namespace, name: r.name, state: '', message: '', clusters: [cluster] }
      e.state = r.missing ? 'Missing' : r.delete ? 'Extra' : 'Modified'
      e.message = r.patch || ''
      out.set(k, e)
    }
  }
  // What isn't ready first: that's what the bundle is opened for.
  const rank = (r: Res) => (r.state === 'Ready' ? 1 : 0)
  return [...out.values()].sort(
    (a, b) => rank(a) - rank(b) || a.kind.localeCompare(b.kind) || (a.namespace || '').localeCompare(b.namespace || '') || a.name.localeCompare(b.name),
  )
}

function resTone(state: string): Tone {
  if (state === 'Ready') return 'good'
  if (state === 'Missing' || state === 'error' || state === 'unavailable') return 'bad'
  return 'busy'
}

// Where an object Fleet deployed is shown: on the host, as YAML; on an
// environment's cluster (CAAPF registers it with Fleet under its own
// namespace and name), in that cluster's explorer.
function objectPath(r: Res): string | undefined {
  const cluster = r.clusters[0]
  if (!cluster || cluster === LOCAL_FLEET_CLUSTER) {
    const type = typeForKind(r.apiVersion, r.kind)
    return type ? paths.yaml(type, r.namespace, r.name) : undefined
  }
  const [ns, name] = cluster.split('/')
  const group = r.apiVersion.includes('/') ? r.apiVersion.split('/')[0] : ''
  const type = group ? `${group}.${r.kind.toLowerCase()}` : r.kind.toLowerCase()
  const q = new URLSearchParams({ type, obj: r.name, ...(r.namespace ? { ns: r.namespace } : {}) })
  return `${paths.cluster(ns, name, 'explorer')}?${q}`
}

// The kinds Helm couldn't find ("no matches for kind X in version Y"), the
// usual reason a bundle fails: a provider or operator that isn't installed.
function missingKinds(message: string): string[] {
  const out = new Set<string>()
  for (const m of message.matchAll(/no matches for kind "([^"]+)" in version "([^"]+)"/g)) out.add(`${m[1]} (${m[2]})`)
  return [...out]
}

export function BundleDetail({ bundle, bds, backLabel }: { bundle: Obj; bds: Obj[]; backLabel: string }) {
  const location = useLocation()
  const back = backFrom(location, backLabel)
  const message = bundleMessage(bundle)
  const missing = missingKinds(message)
  const resources = resourcesOf(bds)
  return (
    <div className="space-y-3 py-2">
      {!!missing.length && (
        <div className="text-sm">
          <p className="mb-1">The cluster doesn't serve these kinds, so nothing in the bundle was applied:</p>
          <ul className="ml-5 list-disc">
            {missing.map((k) => (
              <li key={k} className="font-mono text-xs">
                {k}
              </li>
            ))}
          </ul>
        </div>
      )}
      {message && <CodeBlock className="max-h-48 whitespace-pre-wrap">{message}</CodeBlock>}
      {bds.length > 1 && (
        <div className="flex flex-wrap gap-2 text-sm">
          {bds.map((bd) => (
            <Pill key={bd.metadata.uid} tone={fleetTone(bd.status?.display?.state || '')} label={`${bdCluster(bd)}: ${bd.status?.display?.state || 'Ready'}`} />
          ))}
        </div>
      )}
      {resources.length ? (
        <table className="table">
          <thead>
            <tr>
              <th>Kind</th>
              <th>Namespace</th>
              <th>Name</th>
              <th>State</th>
            </tr>
          </thead>
          <tbody>
            {resources.map((r) => {
              const to = objectPath(r)
              return (
                <tr key={`${r.apiVersion}/${r.kind}/${r.namespace}/${r.name}`}>
                  <td>{r.kind}</td>
                  <td className="text-muted">{r.namespace || '-'}</td>
                  <td className="break-all">
                    {to ? (
                      <Link to={to} state={back}>
                        {r.name}
                      </Link>
                    ) : (
                      r.name
                    )}
                  </td>
                  <td>
                    <Pill tone={resTone(r.state)} label={r.state} />
                    {r.message && <div className="text-muted mt-1 text-xs break-words">{r.message}</div>}
                  </td>
                </tr>
              )
            })}
          </tbody>
        </table>
      ) : (
        <p className="text-muted text-sm">Nothing deployed yet.</p>
      )}
      <Link className="text-sm" to={paths.yaml(FLEET.BUNDLE, bundle.metadata.namespace, bundle.metadata.name)} state={back}>
        Bundle YAML
      </Link>
    </div>
  )
}
