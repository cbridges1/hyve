// One Fleet GitRepo: where it syncs from, then each bundle it made (one per
// directory with a fleet.yaml) with Fleet's state, and, opened, the objects
// that bundle deploys and the state of each. The GitRepo's own YAML is a
// link away; its status lists hundreds of objects, which this page sorts out.
import { Fragment, useState } from 'react'
import { Link, useLocation, useParams } from 'react-router-dom'
import { BundleDetail } from '../components/BundleDetail'
import { Checkbox } from '../components/form'
import { Icon } from '../components/Icon'
import { BackLink, Banner, Card, EmptyState, ErrorBanner, Field, Loading, PageHeader, Pill } from '../components/ui'
import { age } from '../lib/format'
import { useAction, useLoad } from '../lib/hooks'
import { can, patch, type Obj } from '../lib/kube'
import { FLEET } from '../lib/resources'
import { bundleMessage, bundleState, fleetTone as tone, REPO_LABEL, shortBundleName } from '../lib/repos'
import { backFrom, paths } from '../lib/routes'

export default function RepoPage() {
  const { namespace, name } = useParams() as { namespace: string; name: string }
  const location = useLocation()
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
  const short = (b: Obj) => shortBundleName(repo, b)

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
        <Link className="btn btn-secondary" to={paths.yaml(FLEET.GIT_REPO, namespace, name)} state={backFrom(location, `Repo ${name}`)}>
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
                            <BundleDetail bundle={b} bds={bds} backLabel={`Repo ${name}`} />
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
