// One Fleet GitRepo: where it syncs from, then each bundle it made (one per
// directory with a fleet.yaml) with Fleet's state, and, opened, the objects
// that bundle deploys and the state of each. The GitRepo's own YAML is a
// link away; its status lists hundreds of objects, which this page sorts out.
import { Fragment, useState } from 'react'
import { Link, useParams } from 'react-router-dom'
import { Checkbox } from '../components/form'
import { Icon } from '../components/Icon'
import { BackLink, Banner, Card, CodeBlock, EmptyState, ErrorBanner, Field, Loading, PageHeader, Pill } from '../components/ui'
import { age, type Tone } from '../lib/format'
import { useAction, useLoad } from '../lib/hooks'
import { can, patch, typeForKind, type Obj } from '../lib/kube'
import { FLEET } from '../lib/resources'
import { paths } from '../lib/routes'

const REPO_LABEL = 'fleet.cattle.io/repo-name'

// Fleet's bundle and bundle-deployment states.
function tone(state: string): Tone {
  if (!state || state === 'Ready') return 'good'
  if (/^(Err|Error|Failed)/.test(state)) return 'bad'
  if (state === 'OutOfSync' || state === 'Modified' || state === 'NotReady') return 'busy'
  return 'idle'
}

function bundleState(b: Obj): string {
  return b.status?.display?.state || (b.status?.summary && b.status.summary.ready === b.status.summary.desiredReady ? 'Ready' : 'Pending')
}

// Fleet's message (its Ready condition's), without the wrapper it puts
// around a cluster's: "ErrApplied(1) [Cluster fleet-local/local: …]" or
// "NotReady(1) [Cluster fleet-local/local]; …".
function bundleMessage(b: Obj): string {
  if (bundleState(b) === 'Ready') return ''
  const m: string = (b.status?.conditions || []).find((c: any) => c.type === 'Ready' && c.status === 'False')?.message || ''
  return m
    .replace(/^\w+\(\d+\) \[Cluster [^:\]]+\]; /, '')
    .replace(/^\w+\(\d+\) \[Cluster [^:\]]+: (.*)\]$/s, '$1')
    .trim()
}

type Res = { apiVersion: string; kind: string; namespace?: string; name: string; state: string; message: string; clusters: string[] }

// The objects a bundle deploys, from its BundleDeployments (one per target
// cluster): what Fleet applied, with what isn't ready or was changed.
function resourcesOf(bds: Obj[]): Res[] {
  const out = new Map<string, Res>()
  const key = (r: any) => `${r.apiVersion}/${r.kind}/${r.namespace || ''}/${r.name}`
  for (const bd of bds) {
    const cluster = bd.metadata.labels?.['fleet.cattle.io/cluster'] || bd.metadata.namespace || ''
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
  return [...out.values()].sort((a, b) => rank(a) - rank(b) || a.kind.localeCompare(b.kind) ||(a.namespace || '').localeCompare(b.namespace || '') || a.name.localeCompare(b.name))
}

function resTone(state: string): Tone {
  if (state === 'Ready') return 'good'
  if (state === 'Missing' || state === 'error' || state === 'unavailable') return 'bad'
  return 'busy'
}

function ResourceName({ r }: { r: Res }) {
  const type = typeForKind(r.apiVersion, r.kind)
  return type ? <Link to={paths.yaml(type, r.namespace, r.name)}>{r.name}</Link> : <>{r.name}</>
}

// The kinds Helm couldn't find ("no matches for kind X in version Y"), the
// usual reason a bundle fails: a provider or operator that isn't installed.
function missingKinds(message: string): string[] {
  const out = new Set<string>()
  for (const m of message.matchAll(/no matches for kind "([^"]+)" in version "([^"]+)"/g)) out.add(`${m[1]} (${m[2]})`)
  return [...out]
}

function BundleDetail({ bundle, bds }: { bundle: Obj; bds: Obj[] }) {
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
            <Pill
              key={bd.metadata.uid}
              tone={tone(bd.status?.display?.state || '')}
              label={`${bd.metadata.labels?.['fleet.cattle.io/cluster'] || bd.metadata.namespace}: ${bd.status?.display?.state || 'Ready'}`}
            />
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
            {resources.map((r) => (
              <tr key={`${r.apiVersion}/${r.kind}/${r.namespace}/${r.name}`}>
                <td>{r.kind}</td>
                <td className="text-muted">{r.namespace || '-'}</td>
                <td className="break-all">
                  <ResourceName r={r} />
                </td>
                <td>
                  <Pill tone={resTone(r.state)} label={r.state} />
                  {r.message && <div className="text-muted mt-1 text-xs break-words">{r.message}</div>}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      ) : (
        <p className="text-muted text-sm">Nothing deployed yet.</p>
      )}
      <Link className="text-sm" to={paths.yaml(FLEET.BUNDLE, bundle.metadata.namespace, bundle.metadata.name)}>
        Bundle YAML
      </Link>
    </div>
  )
}

export default function RepoPage() {
  const { namespace, name } = useParams() as { namespace: string; name: string }
  const [open, setOpen] = useState<Set<string>>(new Set())
  const [problemsOnly, setProblemsOnly] = useState(false)

  const act = useAction()
  const { data, error, loading, reload } = useLoad(
    async (r) => {
      const [repo, bundles, bds] = await Promise.all([
        r.find(FLEET.GIT_REPO, namespace, name),
        r.findAll(FLEET.BUNDLE, { namespace }).catch(() => [] as Obj[]),
        // Across clusters' namespaces; without the right to list them, the
        // bundles still show, without their objects.
        r.findAll(FLEET.BUNDLE_DEPLOYMENT).catch(() => [] as Obj[]),
      ])
      const mine = bundles.filter((b) => b.metadata.labels?.[REPO_LABEL] === name).sort((a, b) => a.metadata.name.localeCompare(b.metadata.name))
      const byBundle = new Map<string, Obj[]>()
      for (const bd of bds) {
        const l = bd.metadata.labels || {}
        if (l['fleet.cattle.io/bundle-namespace'] !== namespace) continue
        const list = byBundle.get(l['fleet.cattle.io/bundle-name']) || []
        list.push(bd)
        byBundle.set(l['fleet.cattle.io/bundle-name'], list)
      }
      return { repo, bundles: mine, byBundle }
    },
    [namespace, name],
  )

  if (loading) return <Loading />
  if (!data) return <ErrorBanner error={error} />
  const { repo, bundles, byBundle } = data
  const paused = !!repo.spec?.paused
  const update = (body: unknown) =>
    act.run(async () => {
      await patch(FLEET.GIT_REPO, namespace, name, body)
      await reload(true)
    })
  const s = repo.status || {}
  const failing = bundles.filter((b) => bundleState(b) !== 'Ready')
  const shown = problemsOnly ? failing : bundles
  const toggle = (id: string) =>
    setOpen((o) => {
      const n = new Set(o)
      if (n.has(id)) n.delete(id)
      else n.add(id)
      return n
    })
  // Fleet names a bundle REPO-PATH-DIR (fleet/arr → nexus-config-fleet-arr):
  // the directory under the repo's path is what tells them apart.
  const prefixes = [...(repo.spec?.paths || []).map((p: string) => `${name}-${p.replace(/^\/+|\/+$/g, '').replace(/\//g, '-')}-`), `${name}-`]
  const short = (b: Obj) => {
    const p = prefixes.find((x) => b.metadata.name.startsWith(x) && b.metadata.name.length > x.length)
    return p ? b.metadata.name.slice(p.length) : b.metadata.name
  }

  return (
    <div>
      <BackLink to={paths.repos} label="Repos" />
      <PageHeader
        title={
          <span className="flex items-center gap-2">
            {namespace}/{name}
            {paused && <Pill tone="busy" label="Paused" />}
          </span>
        }
        subtitle={repo.spec?.repo}
      >
        {can(FLEET.GIT_REPO, 'patch') && (
          <>
            <button
              type="button"
              className="btn btn-secondary"
              disabled={act.busy}
              title={paused ? 'Deploy what git has again' : "Stop deploying changes from git; what's deployed stays"}
              onClick={() => update({ spec: { paused: !paused } })}
            >
              {paused ? 'Resume' : 'Pause'}
            </button>
            <button
              type="button"
              className="btn btn-secondary"
              disabled={act.busy || paused}
              title="Apply every bundle again, as it is in git (a bundle stuck after a failed upgrade, objects changed by hand)"
              onClick={() => update({ spec: { forceSyncGeneration: (repo.spec?.forceSyncGeneration || 0) + 1 } })}
            >
              Redeploy
            </button>
          </>
        )}
        <Link className="btn btn-secondary" to={paths.yaml(FLEET.GIT_REPO, namespace, name)}>
          YAML
        </Link>
      </PageHeader>
      <ErrorBanner error={error} />
      <ErrorBanner error={act.error} />
      {paused && (
        <Banner tone="warning">
          Paused: Fleet still reads the repo, but doesn't deploy its changes; bundles with changes waiting show as OutOfSync. What's deployed keeps running.
        </Banner>
      )}
      <Card className="mb-5">
        <Field label="Branch">{repo.spec?.revision || repo.spec?.branch || 'main'}</Field>
        {!!repo.spec?.paths?.length && <Field label="Paths">{repo.spec.paths.join(', ')}</Field>}
        <Field label="Commit">
          <span className="font-mono">{(s.commit || '').slice(0, 12) || '-'}</span>
          {s.lastPollingTriggered && <span className="text-muted"> · checked {age(s.lastPollingTriggered)} ago</span>}
        </Field>
        <Field label="Bundles">
          {bundles.length - failing.length}/{bundles.length} ready
        </Field>
        <Field label="Undo changes">{repo.spec?.correctDrift?.enabled ? 'Yes' : 'No'}</Field>
      </Card>
      <Card
        title="Bundles"
        action={<Checkbox label={`Only not ready (${failing.length})`} checked={problemsOnly} onChange={setProblemsOnly} />}
      >
        {!shown.length ? (
          <EmptyState>{problemsOnly ? 'Every bundle is ready.' : 'No bundles yet.'}</EmptyState>
        ) : (
          <div className="overflow-x-auto">
            <table className="table">
              <thead>
                <tr>
                  <th />
                  <th>Bundle</th>
                  <th>Clusters ready</th>
                  <th>State</th>
                  <th>Objects</th>
                </tr>
              </thead>
              <tbody>
                {shown.map((b) => {
                  const id = b.metadata.name
                  const st = bundleState(b)
                  const bds = byBundle.get(id) || []
                  const count = bds[0]?.status?.resources?.length
                  const msg = bundleMessage(b)
                  return (
                    <Fragment key={id}>
                      <tr className="cursor-pointer" onClick={() => toggle(id)}>
                        <td className="w-6">
                          <span className={`inline-block transition-transform ${open.has(id) ? 'rotate-90' : ''}`}>
                            <Icon name="chevronRight" size={14} />
                          </span>
                        </td>
                        <td>
                          {short(b)}
                          {msg && <div className="text-muted line-clamp-1 max-w-xl text-xs">{msg}</div>}
                        </td>
                        <td>{b.status?.display?.readyClusters || '-'}</td>
                        <td>
                          <Pill tone={tone(st)} label={st} />
                        </td>
                        <td>{count ?? '-'}</td>
                      </tr>
                      {open.has(id) && (
                        <tr>
                          <td />
                          <td colSpan={4}>
                            <BundleDetail bundle={b} bds={bds} />
                          </td>
                        </tr>
                      )}
                    </Fragment>
                  )
                })}
              </tbody>
            </table>
          </div>
        )}
      </Card>
    </div>
  )
}
