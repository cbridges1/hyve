// hyve's Users, as `hyve user` manages them: the User object through the
// API, its password through the host. A user's email address is where the
// host sends their password resets and account emails (Settings has the
// SMTP relay); the host emails them when their account is made, or their
// groups or address change, however it was changed. A new user needs an
// address, and is either invited (emailed a link to set their own
// password) or given a password by whoever adds them. Each row says what
// the user may reach (their grants and their groups', lib/access.ts), with
// a link to them on Access. Their environment access can be granted as
// they're made.
import { useState, type FormEvent } from 'react'
import { Link } from 'react-router-dom'
import { ApiTokens } from '../components/ApiTokens'
import { Sessions } from '../components/Sessions'
import { Checkbox, RadioGroup, TextField } from '../components/form'
import { Icon } from '../components/Icon'
import { Modal } from '../components/Modal'
import { Banner, ErrorBanner, Loading, Pill, ConfirmButton, EmptyState } from '../components/ui'
import { TrustedCI } from '../components/TrustedCI'
import { ViewDialog } from '../components/ViewDialog'
import { describeGrant, grantAccess, grantsFor, loadAccess, ROLES, type Grant } from '../lib/access'
import { auth, errorMessage } from '../lib/api'
import { useLoad } from '../lib/hooks'
import { kube, newObject, remove, save, type Obj } from '../lib/kube'
import { paths } from '../lib/routes'
import { useSession } from '../lib/session'
import { TYPES } from '../lib/types'

const NAME_RE = /^[a-z0-9]([-a-z0-9]*[a-z0-9])?$/
const EMAIL_RE = /^[^\s@]+@[^\s@]+$/

type Form = {
  name: string
  email: string
  groups: string
  onboard: 'invite' | 'password' | 'service'
  password: string
  mustChange: boolean
  // The username follows the email until it's edited.
  nameEdited: boolean
  // The environments they're given, each once, with a role.
  access: { env: string; role: string }[]
}

const emptyForm = (): Form => ({
  name: '',
  email: '',
  groups: '',
  onboard: 'invite',
  password: '',
  mustChange: true,
  nameEdited: false,
  access: [],
})

// A username from an email address's local part: "Ada.Lovelace+x@..." is
// "ada-lovelace".
const nameFromEmail = (email: string) =>
  email
    .split('@')[0]
    .split('+')[0]
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-+|-+$/g, '')
    .slice(0, 63)
    .replace(/-+$/, '')

// The roles a new user can be given in an environment: all of a grant's
// but deployer, which isn't per environment.
const ENV_ROLES = ROLES.filter((r) => r.value !== 'deployer').map((r) => ({
  value: r.value,
  label: r.value === 'env-member' ? r.label : `${r.label} of every cluster`,
}))

// On a new User: an invitation is coming, so the host doesn't also send
// "an account was created for you".
const INVITE_ANNOTATION = 'hyve.io/invite'
// A user for CI and scripts (`hyve user create --service`): no password; it
// signs in with API tokens or as a trusted CI job.
const SERVICE_ANNOTATION = 'hyve.io/service'
const isService = (u: Obj) => u.metadata.annotations?.[SERVICE_ANNOTATION] === 'true'

const groupsOf = (s: string) =>
  s
    .split(',')
    .map((g) => g.trim())
    .filter(Boolean)

// The spec with these fields set, leaving out the empty ones. A display
// name isn't asked for; single sign-on fills it from the identity
// provider, and it's kept.
function withFields(spec: any, f: { email: string; groups: string }) {
  const { email: _e, groups: _g, ...rest } = spec || {}
  const groups = groupsOf(f.groups)
  return {
    ...rest,
    ...(f.email.trim() ? { email: f.email.trim() } : {}),
    ...(groups.length ? { groups } : {}),
  }
}

type EditForm = { email: string; groups: string }

// The first few of a user's grants, in words (a group's say which group;
// all of them on hover), and a link to them on Access.
const SHOWN_GRANTS = 3

function AccessSummary({ user, grants }: { user: Obj; grants: Grant[] }) {
  const name = user.metadata.name
  const mine = grantsFor(grants, name, user.spec?.groups || [])
  const say = (g: Grant) => (g.kind === 'group' ? `${describeGrant(g)} (via ${g.who})` : describeGrant(g))
  if (!mine.length) return <div className="mt-0.5 text-xs text-neutral-500">No access granted</div>
  // The link stays visible however long the list: only the list is cut.
  return (
    <div className="mt-0.5 flex min-w-0 gap-1 text-xs text-neutral-500">
      <span className="truncate" title={mine.map(say).join('\n')}>
        {mine.slice(0, SHOWN_GRANTS).map(say).join(' · ')}
        {mine.length > SHOWN_GRANTS && ` · ${mine.length - SHOWN_GRANTS} more`}
      </span>
      <span className="shrink-0">
        ·{' '}
        <Link to={`${paths.access}?user=${encodeURIComponent(name)}`}>Access</Link>
      </span>
    </div>
  )
}

function lastLogin(u: Obj) {
  return u.status?.lastLogin ? `last signed in ${new Date(u.status.lastLogin).toLocaleString()}` : 'never signed in'
}

export default function Users() {
  const me = useSession().user?.username
  // The grants come with the users, so a row's access is current after any
  // change here. Rows say nothing about access to whoever can't read it.
  const { data, error: loadError, loading, reload } = useLoad(async (r) => {
    const [users, access] = await Promise.all([r.findAll(TYPES.USER, { force: true }), loadAccess(r)])
    return {
      users: users.sort((a, b) => a.metadata.name.localeCompare(b.metadata.name)),
      grants: access.grants,
      readable: access.readable,
      envs: access.envs,
    }
  }, [])
  const users = data?.users
  const grants = data?.grants || []
  const envs = data?.envs || []
  const [adding, setAdding] = useState(false)
  const [form, setForm] = useState<Form>(emptyForm)
  const [error, setError] = useState('')
  const [formError, setFormError] = useState('')
  const [busy, setBusy] = useState(false)
  const [viewing, setViewing] = useState<Obj | null>(null)
  const [notice, setNotice] = useState('')
  const [editing, setEditing] = useState<Obj | null>(null)
  const [editForm, setEditForm] = useState<EditForm>({ email: '', groups: '' })
  const [passwordFor, setPasswordFor] = useState('')
  const [tokensFor, setTokensFor] = useState('')
  const [sessionsFor, setSessionsFor] = useState('')
  const [trustFor, setTrustFor] = useState<Obj | null>(null)
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
    const service = f.onboard === 'service'
    const wanted = f.access.filter((a) => a.env && a.role && envs.includes(a.env)).map((a) => [a.env, a.role])
    // Grants follow the user; a failed one leaves the user made, and says
    // so, rather than undoing it.
    const grant = async () => {
      const failed: string[] = []
      for (const [env, role] of wanted) {
        await grantAccess({ kind: 'user', subject: f.name, role, env, cluster: '' }).catch((err) =>
          failed.push(`${env}: ${errorMessage(err)}`),
        )
      }
      if (failed.length) setError(`${f.name} was created, but some access wasn't granted (grant it on Access): ${failed.join('; ')}`)
    }
    if (!(service && !f.email.trim()) && !EMAIL_RE.test(f.email.trim())) {
      setFormError(f.email.trim() ? "That email address doesn't look right" : 'An email address is required')
      return
    }
    if (service) {
      const ok = await act(async () => {
        await save(TYPES.USER, newObject(TYPES.USER, { metadata: { name: f.name, annotations: { [SERVICE_ANNOTATION]: 'true' } }, spec: withFields({}, f) }))
        await grant()
        setNotice(`Created service user ${f.name}. Give it an API token or CI sign-in from its row.`)
      }, setFormError)
      if (ok) {
        setAdding(false)
        setForm(emptyForm())
      }
      return
    }
    const invite = f.onboard === 'invite'
    const ok = await act(async () => {
      const u = await save(
        TYPES.USER,
        newObject(TYPES.USER, {
          metadata: { name: f.name, ...(invite ? { annotations: { [INVITE_ANNOTATION]: 'true' } } : {}) },
          spec: withFields({}, f),
        }),
      )
      if (invite) {
        try {
          await auth.invite(f.name)
        } catch (err) {
          await remove(TYPES.USER, u).catch(() => {})
          throw new Error(`${errorMessage(err)} (the user was removed again)`)
        }
        await grant()
        setNotice(`Invited ${f.name}: they'll get an email at ${f.email.trim()} to set their password. It works for 7 days.`)
        return
      }
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
      await grant()
    }, setFormError)
    if (ok) {
      setAdding(false)
      setForm(emptyForm())
    }
  }

  const reinvite = (u: Obj) =>
    act(async () => {
      await auth.invite(u.metadata.name)
      setNotice(`Sent ${u.metadata.name} another invitation at ${u.spec?.email}.`)
    })

  const setDisabled = (u: Obj, disabled: boolean) => act(() => save(TYPES.USER, { ...u, spec: { ...u.spec, disabled } }))

  function startEdit(u: Obj) {
    setEditing(u)
    setEditForm({ email: u.spec?.email || '', groups: (u.spec?.groups || []).join(', ') })
    setFormError('')
  }

  async function saveEdit(e: FormEvent) {
    e.preventDefault()
    if (!editing) return
    if (editForm.email.trim() && !EMAIL_RE.test(editForm.email.trim())) {
      setFormError("That email address doesn't look right")
      return
    }
    // Fresh, so a sign-in's changes in the meantime aren't undone.
    const ok = await act(async () => {
      const fresh = await kube.find(TYPES.USER, undefined, editing.metadata.name)
      await save(TYPES.USER, { ...fresh, spec: withFields(fresh.spec, editForm) })
    }, setFormError)
    if (ok) setEditing(null)
  }

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
      <div className="mb-4 flex flex-wrap items-center justify-between gap-3">
        <p className="mb-0 max-w-2xl text-sm text-neutral-500">
          Kubernetes sees each user as <code>hyve:&lt;username&gt;</code>, in groups <code>hyve:&lt;group&gt;</code>; grant access on the Access tab,
          with <code>hyve access grant</code> or with RBAC on those names.
        </p>
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
      </div>
      <ErrorBanner error={error || loadError} />
      {notice && (
        <div className="mb-4">
          <Banner tone="success">{notice}</Banner>
        </div>
      )}
      {loading ? (
        <Loading />
      ) : (
        <div className="overflow-hidden rounded-xl border border-neutral-200 bg-white shadow-sm dark:border-neutral-800 dark:bg-neutral-900">
          {!users?.length && <EmptyState>No users yet.</EmptyState>}
          <div className="divide-y divide-neutral-100 dark:divide-neutral-800">
            {users?.map((u) => (
              // The buttons go under the user until there's room beside.
              <div key={u.metadata.name} className="flex flex-col gap-2 px-4 py-3 xl:flex-row xl:items-center xl:justify-between xl:gap-3">
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
                    {isService(u) && <Pill tone="info" label="Service" title="Signs in with API tokens or as trusted CI; no password" />}
                    {u.spec?.trustedCI?.length > 0 && <Pill tone="idle" label={`CI: ${u.spec.trustedCI.length}`} title="CI systems' jobs that sign in as this user" />}
                    {u.spec?.disabled && <Pill tone="bad" label="Disabled" />}
                    {u.spec?.mustChangePassword && <Pill tone="busy" label="Must change password" />}
                  </div>
                  <div className="mt-0.5 truncate text-xs text-neutral-500">
                    {u.spec?.displayName ? `${u.spec.displayName} · ` : ''}
                    {u.spec?.email ? `${u.spec.email} · ` : ''}
                    {lastLogin(u)}
                  </div>
                  {data?.readable && <AccessSummary user={u} grants={grants} />}
                </div>
                <div className="-ml-2.5 flex flex-wrap items-center gap-1 xl:ml-0 xl:shrink-0 xl:justify-end">
                  <button type="button" className="btn btn-tertiary btn-sm" onClick={() => setViewing(u)}>
                    View
                  </button>
                  <button type="button" className="btn btn-tertiary btn-sm" onClick={() => startEdit(u)}>
                    Edit
                  </button>
                  <button type="button" className="btn btn-tertiary btn-sm" title="API tokens for scripts and CI" onClick={() => setTokensFor(u.metadata.name)}>
                    Tokens
                  </button>
                  {!isService(u) && (
                    <button type="button" className="btn btn-tertiary btn-sm" title="Where they're signed in; sign them out" onClick={() => setSessionsFor(u.metadata.name)}>
                      Sessions
                    </button>
                  )}
                  <button type="button" className="btn btn-tertiary btn-sm" title="Let CI jobs sign in with their own ID token" onClick={() => setTrustFor(u)}>
                    CI sign-in
                  </button>
                  {u.metadata.name !== me && !isService(u) && !u.status?.lastLogin && u.spec?.email && !u.spec?.provider && !u.spec?.disabled && (
                    <button
                      type="button"
                      className="btn btn-tertiary btn-sm"
                      disabled={busy}
                      title="Email them a new link to set their password (7 days)"
                      onClick={() => reinvite(u)}
                    >
                      Resend invite
                    </button>
                  )}
                  {u.metadata.name !== me && (
                    <>
                      <button
                        type="button"
                        className="btn btn-tertiary btn-sm"
                        hidden={isService(u)}
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
            <TextField
              className="mb-3"
              label="Email"
              type="email"
              autoFocus
              required={form.onboard !== 'service'}
              value={form.email}
              onChange={(v) => set({ email: v, ...(form.nameEdited ? {} : { name: nameFromEmail(v) }) })}
              help={form.onboard === 'service' ? 'Optional for a service user.' : 'Where their invitation, password resets and account emails go.'}
            />
            <TextField
              className="mb-3"
              label="Username"
              value={form.name}
              onChange={(v) => set({ name: v, nameEdited: true })}
              help={<>Kubernetes sees them as <code>hyve:{form.name || 'username'}</code>.</>}
            />
            <TextField className="mb-3" label="Groups" value={form.groups} onChange={(v) => set({ groups: v })} placeholder="comma-separated" />
            {envs.length > 0 && (
              <fieldset className="mb-3">
                <legend className="mb-1 text-sm font-medium">Environment access</legend>
                {/* Only the environments chosen, each once: a row per grant,
                    so the dialog stays short however many there are. */}
                {form.access.map((a, i) => {
                  const taken = new Set(form.access.filter((_, j) => j !== i).map((x) => x.env))
                  const setRow = (patch: Partial<{ env: string; role: string }>) =>
                    set({ access: form.access.map((x, j) => (j === i ? { ...x, ...patch } : x)) })
                  return (
                    <div key={i} className="mb-2 flex items-center gap-2">
                      <select className="min-w-0 flex-1" aria-label="Environment" value={a.env} onChange={(e) => setRow({ env: e.target.value })}>
                        {envs
                          .filter((e) => e === a.env || !taken.has(e))
                          .map((e) => (
                            <option key={e} value={e}>
                              {e}
                            </option>
                          ))}
                      </select>
                      <select className="min-w-0 flex-1" aria-label={`Role in ${a.env}`} value={a.role} onChange={(e) => setRow({ role: e.target.value })}>
                        {ENV_ROLES.map((r) => (
                          <option key={r.value} value={r.value}>
                            {r.label}
                          </option>
                        ))}
                      </select>
                      <button
                        type="button"
                        className="btn btn-tertiary btn-sm shrink-0"
                        aria-label={`Remove access to ${a.env}`}
                        onClick={() => set({ access: form.access.filter((_, j) => j !== i) })}
                      >
                        <Icon name="close" size={16} />
                      </button>
                    </div>
                  )
                })}
                {form.access.length < envs.length && (
                  <button
                    type="button"
                    className="btn btn-tertiary btn-sm"
                    onClick={() => {
                      const taken = new Set(form.access.map((x) => x.env))
                      const next = envs.find((e) => !taken.has(e))!
                      set({ access: [...form.access, { env: next, role: 'env-member' }] })
                    }}
                  >
                    + Add environment access
                  </button>
                )}
                <span className="hint block">
                  {form.access.length ? '' : 'None yet: they can sign in but reach no clusters. '}More, such as one cluster or deploying to templates, on Access.
                </span>
              </fieldset>
            )}
            <div className="mb-3">
              <RadioGroup
                label="Password"
                value={form.onboard}
                onChange={(v) => set({ onboard: v as Form['onboard'] })}
                options={[
                  { value: 'invite', label: 'Email them a link to set their own password' },
                  { value: 'password', label: 'Set an initial password now' },
                  { value: 'service', label: 'None: a service user for CI and scripts' },
                ]}
              />
            </div>
            {form.onboard === 'service' ? (
              <p className="hint mb-3">It signs in with API tokens or as a trusted CI job (Tokens and CI sign-in on its row), with the access you grant it.</p>
            ) : form.onboard === 'invite' ? (
              <p className="hint mb-3">The link works for 7 days; Resend invite sends another. Email has to be set up in Settings.</p>
            ) : (
              <>
                <TextField
                  className="mb-3"
                  label="Initial password"
                  type="password"
                  autoComplete="new-password"
                  value={form.password}
                  onChange={(v) => set({ password: v })}
                  help="Give it to them yourself; they're emailed that their account exists."
                />
                <div className="mb-3">
                  <Checkbox checked={form.mustChange} onChange={(v) => set({ mustChange: v })} label="Must change it at first sign-in" />
                </div>
              </>
            )}
            {formError && <p className="mb-3 text-sm text-red-600 dark:text-red-400">{formError}</p>}
            <div className="flex justify-end gap-2">
              <button type="button" className="btn btn-tertiary" onClick={() => setAdding(false)}>
                Cancel
              </button>
              <button
                type="submit"
                className="btn btn-primary"
                disabled={busy || !form.name || (form.onboard !== 'service' && !form.email.trim()) || (form.onboard === 'password' && !form.password)}
              >
                {busy ? 'Creating…' : form.onboard === 'invite' ? 'Create and invite' : 'Create'}
              </button>
            </div>
          </form>
        </Modal>
      )}

      {editing && (
        <Modal title={`Edit ${editing.metadata.name}`} onClose={() => setEditing(null)}>
          <form onSubmit={saveEdit}>
            <TextField
              className="mb-3"
              label="Email"
              type="email"
              autoFocus
              value={editForm.email}
              onChange={(v) => setEditForm((f) => ({ ...f, email: v }))}
              disabled={editing.spec?.provider === 'oidc'}
              help={
                editing.spec?.provider === 'oidc'
                  ? 'From the identity provider at each sign-in.'
                  : 'For password resets and account emails. A change is emailed to the old address.'
              }
            />
            <TextField
              className="mb-3"
              label="Groups"
              value={editForm.groups}
              onChange={(v) => setEditForm((f) => ({ ...f, groups: v }))}
              placeholder="comma-separated"
              help="They're emailed when their groups change."
            />
            {formError && <p className="mb-3 text-sm text-red-600 dark:text-red-400">{formError}</p>}
            <div className="flex justify-end gap-2">
              <button type="button" className="btn btn-tertiary" onClick={() => setEditing(null)}>
                Cancel
              </button>
              <button type="submit" className="btn btn-primary" disabled={busy}>
                Save
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

      {sessionsFor && <Sessions user={sessionsFor} own={sessionsFor === me} onClose={() => setSessionsFor('')} />}
      {tokensFor && <ApiTokens user={tokensFor} own={tokensFor === me} onClose={() => setTokensFor('')} />}
      {trustFor && (
        <TrustedCI
          user={trustFor}
          onClose={() => {
            setTrustFor(null)
            reload(true)
          }}
        />
      )}
      {viewing && <ViewDialog title={`User ${viewing.metadata.name}`} sections={[{ label: 'User', object: viewing }]} onClose={() => setViewing(null)} />}
    </div>
  )
}
