// Who can reach which clusters: 'hyve access grant/list/revoke'. A grant
// is a ClusterAccess in the environment (hyve-controller binds the role on
// the clusters), or, for environment members, RoleBindings in the
// environment and the template namespace.
import { useState, type FormEvent } from 'react'
import { SelectField, TextField } from '../components/form'
import { Modal } from '../components/Modal'
import { ConfirmButton, ErrorBanner, Loading, PageHeader } from '../components/ui'
import { ViewDialog } from '../components/ViewDialog'
import { errorMessage } from '../lib/api'
import { useEnvironment } from '../lib/environment'
import { useLoad } from '../lib/hooks'
import { idOf, kube, newObject, remove, save, type Obj } from '../lib/kube'
import { DEFAULT_TEMPLATE_NAMESPACE, LABELS, TYPES } from '../lib/types'

const ROLES = [
  { value: 'owner', label: 'Owner', hint: 'everything on the cluster' },
  { value: 'member', label: 'Member', hint: 'see the cluster, create namespaces' },
  { value: 'read-only', label: 'Read-only', hint: 'read everything but Secrets' },
  { value: 'env-member', label: 'Environment member', hint: 'create and manage clusters in the environment' },
]

const ENV_MEMBER = 'hyve.io/env-member'

type Grant = { id: string; name: string; ns: string; subject: string; role: string; on: string; ready: string; model: Obj; type: string }

type Form = { kind: 'user' | 'group'; subject: string; role: string; env: string; cluster: string }

export default function Access() {
  const [picked] = useEnvironment()
  const { data, error: loadError, loading, reload } = useLoad(async (r) => {
    const none = () => [] as Obj[]
    const [nss, cas, rbs, clusters, users] = await Promise.all([
      r.findAll(TYPES.NAMESPACE, { force: true }).catch(none),
      r.findAll(TYPES.CLUSTER_ACCESS, { force: true }).catch(none),
      r.findAll(TYPES.ROLE_BINDING, { force: true }).catch(none),
      r.findAll(TYPES.CLUSTER, { force: true }).catch(none),
      r.findAll(TYPES.USER, { force: true }).catch(none),
    ])
    const envs = nss
      .filter((n) => n.metadata.labels?.[LABELS.ENVIRONMENT] === 'true')
      .map((n) => n.metadata.name)
      .sort()
    const envSet = new Set(envs)
    const grants: Grant[] = cas.map((ca) => ({
      id: `ca:${idOf(ca)}`,
      name: ca.metadata.name,
      ns: ca.metadata.namespace || '',
      subject: ca.spec?.group ? `group ${ca.spec.group}` : `user ${ca.spec?.user}`,
      role: ca.spec?.role,
      on: ca.spec?.clusterName || 'every cluster',
      ready: (ca.status?.conditions || []).find((c: any) => c.type === 'Ready')?.status || '-',
      model: ca,
      type: TYPES.CLUSTER_ACCESS,
    }))
    rbs
      .filter((rb) => rb.metadata.labels?.[ENV_MEMBER] === 'true' && envSet.has(rb.metadata.namespace || ''))
      .forEach((rb) => {
        ;(rb.subjects || []).forEach((s: any) =>
          grants.push({
            id: `rb:${idOf(rb)}:${s.name}`,
            name: rb.metadata.name,
            ns: rb.metadata.namespace || '',
            subject: `${String(s.kind).toLowerCase()} ${String(s.name).replace(/^hyve:/, '')}`,
            role: 'environment member',
            on: rb.metadata.namespace || '',
            ready: '-',
            model: rb,
            type: TYPES.ROLE_BINDING,
          }),
        )
      })
    grants.sort((a, b) => `${a.ns}/${a.name}`.localeCompare(`${b.ns}/${b.name}`))
    return { envs, grants, clusters, users: users.map((u) => u.metadata.name).sort() }
  }, [])

  const [env, setEnv] = useState(picked)
  const [adding, setAdding] = useState(false)
  const [form, setForm] = useState<Form>({ kind: 'user', subject: '', role: 'owner', env: '', cluster: '' })
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const [viewing, setViewing] = useState<Grant | null>(null)

  const envs = data?.envs || []
  const shown = (data?.grants || []).filter((g) => !env || g.ns === env)
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
    if (!f.subject || !f.env) {
      setError('Give a user or group, and an environment.')
      return
    }
    const subjectKind = f.kind === 'group' ? 'Group' : 'User'
    // The names 'hyve access grant' gives, so the CLI and UI agree.
    const name = `${subjectKind}-${f.subject}`.toLowerCase()
    setBusy(true)
    try {
      if (f.role === 'env-member') {
        for (const [ns, role] of [
          [f.env, 'hyve-environment-member'],
          [DEFAULT_TEMPLATE_NAMESPACE, 'hyve-template-reader'],
        ]) {
          const rbName = `hyve-member-${name}`
          const have = await kube.maybe(TYPES.ROLE_BINDING, ns, rbName)
          const rb =
            have ||
            newObject(TYPES.ROLE_BINDING, {
              metadata: { name: rbName, namespace: ns, labels: { [LABELS.MANAGED_BY]: 'hyve', [ENV_MEMBER]: 'true' } },
            })
          rb.roleRef = { apiGroup: 'rbac.authorization.k8s.io', kind: 'ClusterRole', name: role }
          rb.subjects = [{ apiGroup: 'rbac.authorization.k8s.io', kind: subjectKind, name: `hyve:${f.subject}` }]
          await save(TYPES.ROLE_BINDING, rb)
        }
      } else {
        const spec: Record<string, string> = { role: f.role, [f.kind === 'group' ? 'group' : 'user']: f.subject }
        let caName = `${f.role}-${name}`
        if (f.cluster) {
          spec.clusterName = f.cluster
          caName = `${f.cluster}-${caName}`
        }
        const have = await kube.maybe(TYPES.CLUSTER_ACCESS, f.env, caName)
        const ca = have || newObject(TYPES.CLUSTER_ACCESS, { metadata: { name: caName, namespace: f.env } })
        ca.spec = spec
        await save(TYPES.CLUSTER_ACCESS, ca)
      }
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
      <PageHeader title="Access">
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
      </PageHeader>
      <p className="mb-4 text-sm text-neutral-500">
        Roles on clusters: owner, member or read-only, on one cluster or every cluster in an environment (now and later). A cluster's creator owns it. Also{' '}
        <code>hyve access</code>.
      </p>
      <ErrorBanner error={(!adding && error) || loadError} />
      {loading ? (
        <Loading />
      ) : (
        <table className="table">
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
        </table>
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
              list={form.kind === 'user' ? data?.users : undefined}
            />
            <SelectField
              label="Role"
              value={form.role}
              onChange={(v) => set({ role: v })}
              options={ROLES.map((r) => ({ value: r.value, label: `${r.label} — ${r.hint}` }))}
            />
            <SelectField label="Environment" value={form.env} onChange={(v) => set({ env: v, cluster: '' })} options={envs} />
            {form.role !== 'env-member' && (
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
