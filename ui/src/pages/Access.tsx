// Who can reach which clusters: 'hyve access grant/list/revoke'. A grant
// is a ClusterAccess in the environment (hyve-controller binds the role on
// the clusters), or, for environment members, RoleBindings in the
// environment and the template namespace (lib/access.ts reads them).
// ?user=<name> shows one user's, their groups' included: Users links here.
import { useState, type FormEvent } from 'react'
import { useSearchParams } from 'react-router-dom'
import { SelectField, TextField } from '../components/form'
import { Modal } from '../components/Modal'
import { ConfirmButton, ErrorBanner, Loading } from '../components/ui'
import { ViewDialog } from '../components/ViewDialog'
import { errorMessage } from '../lib/api'
import { useEnvironment } from '../lib/environment'
import { useLoad } from '../lib/hooks'
import { kube, remove } from '../lib/kube'
import { grantAccess, grantsFor, loadAccess, ROLES, type Grant, type GrantRequest } from '../lib/access'
import { DEFAULT_TEMPLATE_NAMESPACE, TYPES } from '../lib/types'
import { Table } from '../components/Table'


type Form = GrantRequest

export default function Access() {
  const [picked] = useEnvironment()
  const { data, error: loadError, loading, reload } = useLoad(loadAccess, [])
  // ?user=<name>: that user's grants, their groups' too (from Users).
  const [params, setParams] = useSearchParams()
  const forUser = params.get('user') || ''

  const [env, setEnv] = useState(picked)
  const [adding, setAdding] = useState(false)
  const [form, setForm] = useState<Form>({ kind: 'user', subject: '', role: 'owner', env: '', cluster: '' })
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const [viewing, setViewing] = useState<Grant | null>(null)

  const envs = data?.envs || []
  const userGroups = data?.users.find((u) => u.metadata.name === forUser)?.spec?.groups || []
  const shown = (forUser ? grantsFor(data?.grants || [], forUser, userGroups) : data?.grants || []).filter((g) => !env || g.ns === env)
  const envClusters = (data?.clusters || [])
    .filter((c) => c.metadata.namespace === form.env)
    .map((c) => c.metadata.name)
    .sort()
  const set = (patch: Partial<Form>) => setForm((f) => ({ ...f, ...patch }))

  function startAdd() {
    setError('')
    setForm({ kind: 'user', subject: '', role: 'owner', env: env || envs[0] || '', cluster: '' })
    setAdding(true)
  }

  async function grant(e: FormEvent) {
    e.preventDefault()
    const f = form
    setError('')
    if (!f.subject || (!f.env && f.role !== 'deployer')) {
      setError('Give a user or group, and an environment.')
      return
    }
    setBusy(true)
    try {
      await grantAccess(f)
      setAdding(false)
      await reload(true)
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }

  async function revoke(g: Grant) {
    setError('')
    try {
      await remove(g.type, g.model)
      if (g.id.startsWith('rb:')) {
        // Membership is two RoleBindings: the environment's and the
        // template namespace's.
        const other = await kube.maybe(TYPES.ROLE_BINDING, DEFAULT_TEMPLATE_NAMESPACE, g.name)
        if (other) await remove(TYPES.ROLE_BINDING, other)
      }
      await reload(true)
    } catch (err) {
      setError(errorMessage(err))
    }
  }

  return (
    <div>
      <div className="mb-4 flex flex-wrap items-center justify-between gap-3">
        <p className="mb-0 max-w-2xl text-sm text-neutral-500">
          Roles on clusters: owner, member or read-only, on one cluster or every cluster in an environment (now and later). A cluster's creator owns it.
          Environment members make clusters; deployers (CI) change the Deployments aimed at templates. Also{' '}
          <code>hyve access</code>.
        </p>
        <div className="flex flex-wrap items-center gap-2.5">
          <select className="w-auto!" aria-label="Environment" value={env} onChange={(e) => setEnv(e.target.value)}>
            <option value="">All environments</option>
            {envs.map((e) => (
              <option key={e} value={e}>
                {e}
              </option>
            ))}
          </select>
          <button type="button" className="btn btn-primary" onClick={startAdd}>
            Grant access
          </button>
        </div>
      </div>
      {forUser && (
        <div className="mb-4 flex flex-wrap items-center gap-2 text-sm text-neutral-600 dark:text-neutral-400">
          <span>
            Showing <span className="font-medium text-neutral-900 dark:text-neutral-100">{forUser}</span>'s grants
            {userGroups.length > 0 && ` and their groups' (${userGroups.join(', ')})`}.
          </span>
          <button type="button" className="btn btn-tertiary btn-sm" onClick={() => setParams({})}>
            Show everyone
          </button>
        </div>
      )}
      <ErrorBanner error={(!adding && error) || loadError} />
      {loading ? (
        <Loading />
      ) : (
        <div className="overflow-x-auto">
          <Table>
            <thead>
              <tr>
                <th>Subject</th>
                <th>Role</th>
                <th>Environment</th>
                <th>On</th>
                <th>Ready</th>
                <th />
              </tr>
            </thead>
            <tbody>
              {!shown.length && (
                <tr>
                  <td colSpan={6} className="text-muted">
                    No grants here.
                  </td>
                </tr>
              )}
              {shown.map((g) => (
                <tr key={g.id}>
                  <td>{g.subject}</td>
                  <td>{g.role}</td>
                  <td>{g.ns}</td>
                  <td>{g.on}</td>
                  <td>{g.ready}</td>
                  <td className="text-right whitespace-nowrap">
                    <button type="button" className="btn btn-tertiary btn-sm" onClick={() => setViewing(g)}>
                      View
                    </button>
                    <ConfirmButton label="Revoke" confirm={`Revoke ${g.role} for ${g.subject}?`} onConfirm={() => revoke(g)} />
                  </td>
                </tr>
              ))}
            </tbody>
          </Table>
        </div>
      )}

      {adding && (
        <Modal title="Grant access" onClose={() => setAdding(false)}>
          <form className="space-y-3" onSubmit={grant}>
            <div className="flex gap-4 text-sm">
              {(['user', 'group'] as const).map((k) => (
                <label key={k} className="flex cursor-pointer items-center gap-2">
                  <input type="radio" name="access-kind" checked={form.kind === k} onChange={() => set({ kind: k })} />
                  {k === 'user' ? 'User' : 'Group'}
                </label>
              ))}
            </div>
            <TextField
              label={form.kind === 'group' ? 'Group' : 'User'}
              value={form.subject}
              onChange={(v) => set({ subject: v })}
              autoComplete="off"
              list={form.kind === 'user' ? data?.users.map((u) => u.metadata.name) : undefined}
            />
            <SelectField
              label="Role"
              value={form.role}
              onChange={(v) => set({ role: v })}
              options={ROLES.map((r) => ({ value: r.value, label: `${r.label} — ${r.hint}` }))}
            />
            {form.role === 'deployer' ? (
              <p className="hint">In the template namespace ({DEFAULT_TEMPLATE_NAMESPACE}), where the Deployments aimed at templates, classes and all clusters live.</p>
            ) : (
              <SelectField label="Environment" value={form.env} onChange={(v) => set({ env: v, cluster: '' })} options={envs} />
            )}
            {form.role !== 'env-member' && form.role !== 'deployer' && (
              <SelectField
                label="Cluster"
                value={form.cluster}
                onChange={(v) => set({ cluster: v })}
                placeholder={`Every cluster in ${form.env || 'the environment'}`}
                options={envClusters}
              />
            )}
            {error && <p className="text-sm text-red-600 dark:text-red-400">{error}</p>}
            <div className="flex justify-end gap-2">
              <button type="button" className="btn btn-tertiary" onClick={() => setAdding(false)}>
                Cancel
              </button>
              <button type="submit" className="btn btn-primary" disabled={busy}>
                Grant
              </button>
            </div>
          </form>
        </Modal>
      )}
      {viewing && (
        <ViewDialog title={viewing.name} sections={[{ label: viewing.model.kind || 'Object', object: viewing.model }]} onClose={() => setViewing(null)} />
      )}
    </div>
  )
}
