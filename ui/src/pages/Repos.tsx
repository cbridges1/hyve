// Fleet GitRepos (not the ones hyve makes for template resources) and
// their bundles. fleet-local deploys to the management cluster itself:
// ClusterClasses, templates, anything else it runs. Adding and editing do
// what 'hyve repo add' does, credentials for a private repo included (a
// basic-auth Secret the GitRepo names).
import { useState } from 'react'
import { Card, ErrorBanner, Loading, PageHeader } from '../components/ui'
import { Checkbox, TextField } from '../components/form'
import { ViewDialog } from '../components/ViewDialog'
import { useAction, useLoad } from '../lib/hooks'
import { idOf, kube, newObject, remove, save, type Obj } from '../lib/kube'
import { FLEET, problem } from '../lib/resources'
import { LABELS } from '../lib/types'

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

function emptyForm(): Form {
  return { name: '', namespace: 'fleet-local', repo: '', branch: 'main', revision: '', paths: '', correctDrift: false, username: '', token: '', editing: null }
}

function b64(s: string) {
  return btoa(String.fromCharCode(...new TextEncoder().encode(s)))
}

function bundles(r: Obj) {
  const s = r.status?.summary
  return s ? `${s.ready || 0}/${s.desiredReady || 0}` : '-'
}

function state(r: Obj) {
  return problem(r) || r.status?.display?.state || (r.status?.summary?.ready === r.status?.summary?.desiredReady ? 'Ready' : 'Pending')
}

export default function Repos() {
  const { data: repos, error, loading, reload } = useLoad(async (r) => {
    const all = await r.findAll(FLEET.GIT_REPO).catch(() => [] as Obj[])
    return all.filter((g) => !g.metadata.labels?.[LABELS.OWNER_KIND]).sort((a, b) => idOf(a).localeCompare(idOf(b)))
  }, [])
  const [adding, setAdding] = useState(false)
  const [form, setForm] = useState<Form>(emptyForm)
  const [viewing, setViewing] = useState<Obj | null>(null)
  const act = useAction()
  const set = (p: Partial<Form>) => setForm((f) => ({ ...f, ...p }))

  const startAdd = () => {
    act.setError('')
    setForm(emptyForm())
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
      setForm(emptyForm())
      await reload(true)
    })

  const removeRepo = (r: Obj) =>
    act.run(async () => {
      await remove(FLEET.GIT_REPO, r)
      await reload(true)
    })

  if (loading) return <Loading />
  return (
    <div>
      <PageHeader
        title="Repos"
        subtitle="Fleet GitRepos. In fleet-local they deploy to this management cluster (ClusterClasses, templates); other workspaces target their clusters."
      >
        {!adding && (
          <button type="button" className="btn btn-primary" onClick={startAdd}>
            Add repo
          </button>
        )}
      </PageHeader>
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
              disabled={!!form.editing}
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
              <th>Workspace</th>
              <th>Repository URL</th>
              <th>Branch</th>
              <th>Bundles ready</th>
              <th>State</th>
              <th />
            </tr>
          </thead>
          <tbody>
            {!repos?.length && (
              <tr>
                <td colSpan={7} className="text-muted">
                  No repos yet.
                </td>
              </tr>
            )}
            {repos?.map((r) => (
              <tr key={idOf(r)}>
                <td>{r.metadata.name}</td>
                <td>{r.metadata.namespace}</td>
                <td className="break-all">
                  {r.spec?.repo}
                  {!!r.spec?.paths?.length && <div className="text-muted text-xs">{r.spec.paths.join(', ')}</div>}
                </td>
                <td>{r.spec?.branch || r.spec?.revision || '-'}</td>
                <td>{bundles(r)}</td>
                <td>{state(r)}</td>
                <td className="text-right whitespace-nowrap">
                  <button type="button" className="btn btn-tertiary btn-sm" onClick={() => setViewing(r)}>
                    View
                  </button>
                  <button type="button" className="btn btn-tertiary btn-sm" onClick={() => startEdit(r)}>
                    Edit
                  </button>
                  <button type="button" className="btn btn-tertiary btn-sm" disabled={act.busy} onClick={() => removeRepo(r)}>
                    Remove
                  </button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      {viewing && (
        <ViewDialog
          title={`GitRepo ${viewing.metadata.namespace}/${viewing.metadata.name}`}
          sections={[{ label: 'GitRepo', object: viewing }]}
          onClose={() => setViewing(null)}
        />
      )}
    </div>
  )
}
