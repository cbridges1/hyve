// Fleet GitRepos people add (not the ones hyve makes for template
// resources): the table and the add/edit form, on the Repos page (every
// repo) and on a cluster's Resources tab (scope: the repos that deliver to
// it). Adding and editing do what 'hyve repo add' does, credentials for a
// private repo included (a basic-auth Secret the GitRepo names). A repo
// added on a cluster's tab goes in its Fleet workspace and targets it.
import { useState, type ReactNode } from 'react'
import { Link } from 'react-router-dom'
import { useAction, useLoad } from '../lib/hooks'
import { idOf, kube, newObject, patch, remove, save, type Obj } from '../lib/kube'
import { isUserRepo, repoBundles } from '../lib/repos'
import { FLEET, problem } from '../lib/resources'
import { paths } from '../lib/routes'
import { LABELS } from '../lib/types'
import { Checkbox, TextField } from './form'
import { Card, ConfirmButton, ErrorBanner, Loading, Pill } from './ui'

// A cluster, as Fleet knows it: its workspace (the namespace its Fleet
// Cluster is in: fleet-local for the host, an environment for a child)
// and, for a child, its name, which a repo added here targets.
export type RepoScope = { workspace: string; clusterName?: string; fleetId: string }

const NAME_RE = /^[a-z0-9]([-a-z0-9]*[a-z0-9])?$/

type Form = {
  name: string
  namespace: string
  repo: string
  branch: string
  revision: string
  paths: string
  correctDrift: boolean
  username: string
  token: string
  editing: Obj | null
}

function emptyForm(workspace = 'fleet-local'): Form {
  return { name: '', namespace: workspace, repo: '', branch: 'main', revision: '', paths: '', correctDrift: false, username: '', token: '', editing: null }
}

function b64(s: string) {
  return btoa(String.fromCharCode(...new TextEncoder().encode(s)))
}

function bundles(r: Obj) {
  const s = r.status?.summary
  return s ? `${s.ready || 0}/${s.desiredReady || 0}` : '-'
}

// Fleet's state as a pill, with the first line of its message; the repo's
// page has each bundle's in full.
function State({ r }: { r: Obj }) {
  const s = r.status?.display?.state || (r.status?.summary?.ready === r.status?.summary?.desiredReady ? 'Ready' : 'Pending')
  const msg = problem(r)
  const tone = s === 'Ready' && !msg ? 'good' : /^(Err|Error|Failed)/.test(s) || r.status?.display?.error ? 'bad' : 'busy'
  return (
    <>
      <Pill tone={tone} label={s} />
      {r.spec?.paused && (
        <span className="ml-1">
          <Pill tone="busy" label="Paused" />
        </span>
      )}
      {msg && (
        <div className="text-muted mt-1 line-clamp-2 max-w-md text-xs" title={msg}>
          {msg.replace(/^Error: /, '')}
        </div>
      )}
    </>
  )
}

// The repos a cluster gets bundles from: any with a bundle there, and
// those whose targets name it (before their first bundle).
function inScope(scope: RepoScope, r: Obj, withBundles: Set<string>) {
  if (withBundles.has(idOf(r))) return true
  if (r.metadata.namespace !== scope.workspace) return false
  if (!scope.clusterName) return true // fleet-local: every repo there targets the host
  return (r.spec?.targets || []).some((t: any) => t.clusterName === scope.clusterName)
}

export function RepoManager({ scope, header }: { scope?: RepoScope; header?: (add: ReactNode) => ReactNode }) {
  const { data, error, loading, reload } = useLoad(async (r) => {
    const [all, fromGit] = await Promise.all([r.findAll(FLEET.GIT_REPO).catch(() => [] as Obj[]), scope ? repoBundles(r) : Promise.resolve([])])
    // Per repo, its bundles on the scope's cluster: how many, how many ready.
    const here = new Map<string, { ready: number; total: number }>()
    if (scope)
      for (const g of fromGit) {
        const state = g.on[scope.fleetId]
        if (!state) continue
        const h = here.get(idOf(g.repo)) || { ready: 0, total: 0 }
        h.total++
        if (state === 'Ready') h.ready++
        here.set(idOf(g.repo), h)
      }
    const repos = all
      .filter(isUserRepo)
      .filter((g) => !scope || inScope(scope, g, new Set(here.keys())))
      .sort((a, b) => idOf(a).localeCompare(idOf(b)))
    return { repos, here }
  }, [scope?.fleetId])
  const repos = data?.repos
  const [adding, setAdding] = useState(false)
  const [form, setForm] = useState<Form>(() => emptyForm(scope?.workspace))
  const act = useAction()
  const set = (p: Partial<Form>) => setForm((f) => ({ ...f, ...p }))

  const startAdd = () => {
    act.setError('')
    setForm(emptyForm(scope?.workspace))
    setAdding(true)
  }

  const startEdit = (r: Obj) => {
    act.setError('')
    setForm({
      ...emptyForm(),
      name: r.metadata.name,
      namespace: r.metadata.namespace || '',
      repo: r.spec?.repo || '',
      branch: r.spec?.branch || '',
      revision: r.spec?.revision || '',
      paths: (r.spec?.paths || []).join(', '),
      correctDrift: !!r.spec?.correctDrift?.enabled,
      editing: r,
    })
    setAdding(true)
  }

  const submit = () =>
    act.run(async () => {
      const f = form
      if (!NAME_RE.test(f.name)) throw new Error('Name: lowercase letters, digits and dashes')
      if (!f.repo) throw new Error('The repository URL is required')
      if (f.token && !f.username) throw new Error('A token needs a username.')
      const ns = f.namespace || 'fleet-local'
      let model = f.editing ? await kube.find(FLEET.GIT_REPO, ns, f.name) : null
      const spec: any = { ...(model?.spec || {}), repo: f.repo }
      for (const k of ['branch', 'revision'] as const) {
        if (f[k]) spec[k] = f[k]
        else delete spec[k]
      }
      if (!spec.branch && !spec.revision) spec.branch = 'main'
      const paths = f.paths
        .split(',')
        .map((s) => s.trim())
        .filter(Boolean)
      if (paths.length) spec.paths = paths
      else delete spec.paths
      if (f.correctDrift) spec.correctDrift = { ...(spec.correctDrift || {}), enabled: true }
      else delete spec.correctDrift
      // Added on a child cluster's tab: that cluster only. Its targets are
      // edited in its YAML after that.
      if (!model && scope?.clusterName) spec.targets = [{ clusterName: scope.clusterName }]
      if (f.token) {
        // The same Secret 'hyve repo add --username --token-env' makes.
        const secretName = `${f.name}-git-auth`
        const secret =
          (await kube.maybe('secret', ns, secretName)) ||
          newObject('secret', { metadata: { name: secretName, namespace: ns }, type: 'kubernetes.io/basic-auth' })
        secret.data = { username: b64(f.username), password: b64(f.token) }
        await save('secret', secret)
        spec.clientSecretName = secretName
      }
      if (!model) {
        model = newObject(FLEET.GIT_REPO, { metadata: { name: f.name, namespace: ns, labels: { [LABELS.MANAGED_BY]: 'hyve' } } })
      }
      await save(FLEET.GIT_REPO, { ...model, spec })
      setAdding(false)
      setForm(emptyForm(scope?.workspace))
      await reload(true)
    })

  const removeRepo = (r: Obj) =>
    act.run(async () => {
      await remove(FLEET.GIT_REPO, r)
      await reload(true)
    })

  const setPaused = (r: Obj, paused: boolean) =>
    act.run(async () => {
      await patch(FLEET.GIT_REPO, r.metadata.namespace, r.metadata.name, { spec: { paused } })
      await reload(true)
    })

  if (loading) return <Loading />
  const add = !adding && (
    <button type="button" className={scope ? 'btn btn-secondary btn-sm' : 'btn btn-primary'} onClick={startAdd}>
      Add repo
    </button>
  )
  return (
    <div>
      {header ? header(add) : add}
      <ErrorBanner error={error} />
      <ErrorBanner error={act.error} />
      {adding && (
        <Card title={form.editing ? `Edit ${form.name}` : 'Add repo'} className="mb-5">
          <div className="grid grid-cols-1 gap-4 sm:grid-cols-12">
            <TextField className="sm:col-span-3" label="Name" required value={form.name} disabled={!!form.editing} onChange={(v) => set({ name: v })} />
            <TextField
              className="sm:col-span-3"
              label="Workspace"
              value={form.namespace}
              disabled={!!form.editing || !!scope}
              onChange={(v) => set({ namespace: v })}
            />
            <TextField
              className="sm:col-span-6"
              label="Repository URL"
              required
              placeholder="https://github.com/org/repo.git"
              value={form.repo}
              onChange={(v) => set({ repo: v })}
            />
            <TextField className="sm:col-span-3" label="Branch" value={form.branch} onChange={(v) => set({ branch: v })} />
            <TextField className="sm:col-span-3" label="Revision (commit or tag)" value={form.revision} onChange={(v) => set({ revision: v })} />
            <TextField
              className="sm:col-span-6"
              label="Paths"
              placeholder="comma-separated directories with a fleet.yaml (default the root)"
              value={form.paths}
              onChange={(v) => set({ paths: v })}
            />
            <TextField className="sm:col-span-3" label="Git username" autoComplete="off" value={form.username} onChange={(v) => set({ username: v })} />
            <TextField
              className="sm:col-span-5"
              label="Git token"
              type="password"
              autoComplete="new-password"
              placeholder={form.editing?.spec?.clientSecretName ? 'leave empty to keep the saved one' : ''}
              value={form.token}
              onChange={(v) => set({ token: v })}
            />
            <div className="flex items-end pb-2 sm:col-span-4">
              <Checkbox label="Undo changes made on clusters" checked={form.correctDrift} onChange={(v) => set({ correctDrift: v })} />
            </div>
          </div>
          <p className="text-muted my-4 text-sm">
            A private repo needs a username and a token; hyve keeps them in a basic-auth Secret (NAME-git-auth) in the workspace, which the GitRepo uses.
            {!form.editing && scope?.clusterName && ` It deploys to ${scope.clusterName} only; to add clusters, edit its targets in its YAML.`}
            {form.editing && scope && ' This is the repo itself: a change here applies on every cluster it deploys to.'}
          </p>
          <div className="flex gap-2.5">
            <button type="button" className="btn btn-primary" disabled={act.busy} onClick={submit}>
              {form.editing ? 'Save' : 'Add'}
            </button>
            <button type="button" className="btn btn-secondary" onClick={() => setAdding(false)}>
              Cancel
            </button>
          </div>
        </Card>
      )}
      <div className="overflow-x-auto">
        <table className="table">
          <thead>
            <tr>
              <th>Name</th>
              {!scope && <th>Workspace</th>}
              <th>Repository URL</th>
              <th>Branch</th>
              <th>{scope ? 'Bundles ready here' : 'Bundles ready'}</th>
              <th>State</th>
              <th />
            </tr>
          </thead>
          <tbody>
            {!repos?.length && (
              <tr>
                <td colSpan={7} className="text-muted">
                  {scope ? 'No repos deliver here yet.' : 'No repos yet.'}
                </td>
              </tr>
            )}
            {repos?.map((r) => {
              const h = data?.here.get(idOf(r))
              return (
                <tr key={idOf(r)}>
                  <td>
                    <Link to={paths.repo(r.metadata.namespace!, r.metadata.name)}>{r.metadata.name}</Link>
                    {scope && <div className="text-muted text-xs">{r.metadata.namespace}</div>}
                  </td>
                  {!scope && <td>{r.metadata.namespace}</td>}
                  <td className="break-all sm:min-w-64">
                    {r.spec?.repo}
                    {!!r.spec?.paths?.length && <div className="text-muted text-xs">{r.spec.paths.join(', ')}</div>}
                  </td>
                  <td>{r.spec?.branch || r.spec?.revision || '-'}</td>
                  <td>{scope ? (h ? `${h.ready}/${h.total}` : '-') : bundles(r)}</td>
                  <td>
                    <State r={r} />
                  </td>
                  <td className="text-right whitespace-nowrap">
                    <button type="button" className="btn btn-tertiary btn-sm" onClick={() => startEdit(r)}>
                      Edit
                    </button>
                    <button
                      type="button"
                      className="btn btn-tertiary btn-sm"
                      disabled={act.busy}
                      title={r.spec?.paused ? 'Deploy what git has again' : "Stop deploying changes from git; what's deployed stays"}
                      onClick={() => setPaused(r, !r.spec?.paused)}
                    >
                      {r.spec?.paused ? 'Resume' : 'Pause'}
                    </button>
                    <ConfirmButton
                      label="Remove"
                      confirm={`Remove ${r.metadata.name}? Fleet removes what it deployed, on every cluster.`}
                      disabled={act.busy}
                      onConfirm={() => removeRepo(r)}
                    />
                  </td>
                </tr>
              )
            })}
          </tbody>
        </table>
      </div>
    </div>
  )
}
