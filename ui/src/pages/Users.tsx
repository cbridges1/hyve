// hyve's Users, as `hyve user` manages them: the User object through the
// API, its password through the host.
import { useState, type FormEvent } from 'react'
import { Checkbox, TextField } from '../components/form'
import { Modal } from '../components/Modal'
import { ErrorBanner, Loading, PageHeader, Pill, ConfirmButton, EmptyState } from '../components/ui'
import { ViewDialog } from '../components/ViewDialog'
import { auth, errorMessage } from '../lib/api'
import { useLoad } from '../lib/hooks'
import { kube, newObject, remove, save, type Obj } from '../lib/kube'
import { useSession } from '../lib/session'
import { TYPES } from '../lib/types'

const NAME_RE = /^[a-z0-9]([-a-z0-9]*[a-z0-9])?$/

type Form = { name: string; displayName: string; groups: string; password: string; mustChange: boolean }

const emptyForm = (): Form => ({ name: '', displayName: '', groups: '', password: '', mustChange: true })

function lastLogin(u: Obj) {
  return u.status?.lastLogin ? `last signed in ${new Date(u.status.lastLogin).toLocaleString()}` : 'never signed in'
}

export default function Users() {
  const me = useSession().user?.username
  const { data: users, error: loadError, loading, reload } = useLoad(
    async (r) => (await r.findAll(TYPES.USER, { force: true })).sort((a, b) => a.metadata.name.localeCompare(b.metadata.name)),
    [],
  )
  const [adding, setAdding] = useState(false)
  const [form, setForm] = useState<Form>(emptyForm)
  const [error, setError] = useState('')
  const [formError, setFormError] = useState('')
  const [busy, setBusy] = useState(false)
  const [viewing, setViewing] = useState<Obj | null>(null)
  const [passwordFor, setPasswordFor] = useState('')
  const [newPassword, setNewPassword] = useState('')

  const set = (patch: Partial<Form>) => setForm((f) => ({ ...f, ...patch }))

  async function act(fn: () => Promise<unknown>, onError: (m: string) => void = setError) {
    setError('')
    setFormError('')
    setBusy(true)
    try {
      await fn()
      await reload(true)
      return true
    } catch (e) {
      onError(errorMessage(e))
      return false
    } finally {
      setBusy(false)
    }
  }

  async function add(e: FormEvent) {
    e.preventDefault()
    const f = form
    if (!NAME_RE.test(f.name) || f.name.length > 63) {
      setFormError("A username is a DNS label: lowercase letters, digits and '-', at most 63")
      return
    }
    const ok = await act(async () => {
      const groups = f.groups
        .split(',')
        .map((g) => g.trim())
        .filter(Boolean)
      const u = await save(
        TYPES.USER,
        newObject(TYPES.USER, {
          metadata: { name: f.name },
          spec: { ...(f.displayName ? { displayName: f.displayName } : {}), ...(groups.length ? { groups } : {}) },
        }),
      )
      try {
        await auth.password(f.name, '', f.password)
      } catch (err) {
        await remove(TYPES.USER, u).catch(() => {})
        throw new Error(`${errorMessage(err)} (the user was removed again)`)
      }
      // Setting a password clears mustChangePassword, so set it after.
      if (f.mustChange) {
        const fresh = await kube.find(TYPES.USER, undefined, f.name)
        fresh.spec = { ...fresh.spec, mustChangePassword: true }
        await save(TYPES.USER, fresh)
      }
    }, setFormError)
    if (ok) {
      setAdding(false)
      setForm(emptyForm())
    }
  }

  const setDisabled = (u: Obj, disabled: boolean) => act(() => save(TYPES.USER, { ...u, spec: { ...u.spec, disabled } }))

  async function setPassword(e: FormEvent) {
    e.preventDefault()
    const ok = await act(() => auth.password(passwordFor, '', newPassword), setFormError)
    if (ok) {
      setPasswordFor('')
      setNewPassword('')
    }
  }

  return (
    <div>
      <PageHeader title="Users">
        <button
          type="button"
          className="btn btn-primary"
          onClick={() => {
            setAdding(true)
            setFormError('')
          }}
        >
          New user
        </button>
      </PageHeader>
      <p className="mb-4 text-sm text-neutral-500">
        Kubernetes sees each user as <code>hyve:&lt;username&gt;</code>, in groups <code>hyve:&lt;group&gt;</code>; grant access with{' '}
        <code>hyve access grant</code> or RBAC on those names.
      </p>
      <ErrorBanner error={error || loadError} />
      {loading ? (
        <Loading />
      ) : (
        <div className="overflow-hidden rounded-xl border border-neutral-200 bg-white shadow-sm dark:border-neutral-800 dark:bg-neutral-900">
          {!users?.length && <EmptyState>No users yet.</EmptyState>}
          <div className="divide-y divide-neutral-100 dark:divide-neutral-800">
            {users?.map((u) => (
              <div key={u.metadata.name} className="flex items-center justify-between gap-3 px-4 py-3">
                <div className="min-w-0 flex-1">
                  <div className="flex flex-wrap items-center gap-2">
                    <span className="font-medium text-neutral-900 dark:text-neutral-100">{u.metadata.name}</span>
                    {u.metadata.name === me && <span className="text-xs text-neutral-400">(you)</span>}
                    {(u.spec?.groups || []).map((g: string) => (
                      <span
                        key={g}
                        className="rounded-full bg-neutral-100 px-2 py-0.5 text-xs font-medium text-neutral-600 dark:bg-neutral-800 dark:text-neutral-400"
                      >
                        {g}
                      </span>
                    ))}
                    {u.spec?.disabled && <Pill tone="bad" label="Disabled" />}
                    {u.spec?.mustChangePassword && <Pill tone="busy" label="Must change password" />}
                  </div>
                  <div className="mt-0.5 truncate text-xs text-neutral-500">
                    {u.spec?.displayName ? `${u.spec.displayName} · ` : ''}
                    {lastLogin(u)}
                  </div>
                </div>
                <div className="flex shrink-0 items-center gap-1">
                  <button type="button" className="btn btn-tertiary btn-sm" onClick={() => setViewing(u)}>
                    View
                  </button>
                  {u.metadata.name !== me && (
                    <>
                      <button
                        type="button"
                        className="btn btn-tertiary btn-sm"
                        onClick={() => {
                          setPasswordFor(u.metadata.name)
                          setNewPassword('')
                          setFormError('')
                        }}
                      >
                        Set password
                      </button>
                      <button type="button" className="btn btn-tertiary btn-sm" disabled={busy} onClick={() => setDisabled(u, !u.spec?.disabled)}>
                        {u.spec?.disabled ? 'Enable' : 'Disable'}
                      </button>
                      <ConfirmButton label="Delete" confirm={`Delete ${u.metadata.name}?`} onConfirm={() => act(() => remove(TYPES.USER, u))} />
                    </>
                  )}
                </div>
              </div>
            ))}
          </div>
        </div>
      )}

      {adding && (
        <Modal title="New user" onClose={() => setAdding(false)}>
          <form onSubmit={add}>
            <div className="mb-3 grid gap-3 sm:grid-cols-2">
              <TextField label="Username" value={form.name} onChange={(v) => set({ name: v })} autoFocus />
              <TextField label="Display name" value={form.displayName} onChange={(v) => set({ displayName: v })} />
            </div>
            <TextField className="mb-3" label="Groups" value={form.groups} onChange={(v) => set({ groups: v })} placeholder="comma-separated" />
            <TextField
              className="mb-3"
              label="Initial password"
              type="password"
              autoComplete="new-password"
              value={form.password}
              onChange={(v) => set({ password: v })}
            />
            <div className="mb-3">
              <Checkbox checked={form.mustChange} onChange={(v) => set({ mustChange: v })} label="Must change it at first sign-in" />
            </div>
            {formError && <p className="mb-3 text-sm text-red-600 dark:text-red-400">{formError}</p>}
            <div className="flex justify-end gap-2">
              <button type="button" className="btn btn-tertiary" onClick={() => setAdding(false)}>
                Cancel
              </button>
              <button type="submit" className="btn btn-primary" disabled={busy || !form.name || !form.password}>
                {busy ? 'Creating…' : 'Create'}
              </button>
            </div>
          </form>
        </Modal>
      )}

      {passwordFor && (
        <Modal title={`Set ${passwordFor}'s password`} onClose={() => setPasswordFor('')}>
          <form onSubmit={setPassword}>
            <TextField
              className="mb-3"
              label="New password"
              type="password"
              autoComplete="new-password"
              autoFocus
              value={newPassword}
              onChange={setNewPassword}
              help="Signs them out everywhere."
            />
            {formError && <p className="mb-3 text-sm text-red-600 dark:text-red-400">{formError}</p>}
            <div className="flex justify-end gap-2">
              <button type="button" className="btn btn-tertiary" onClick={() => setPasswordFor('')}>
                Cancel
              </button>
              <button type="submit" className="btn btn-primary" disabled={busy || !newPassword}>
                Set password
              </button>
            </div>
          </form>
        </Modal>
      )}

      {viewing && <ViewDialog title={`User ${viewing.metadata.name}`} sections={[{ label: 'User', object: viewing }]} onClose={() => setViewing(null)} />}
    </div>
  )
}
