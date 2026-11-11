// The CI systems' jobs a user trusts (spec.trustedCI, `hyve user trust`):
// a job whose ID token is from the issuer, for the audience, with the
// claims, signs in as the user for an hour, keeping no secret. One claim
// must pin the repository or project, as the host checks (it ignores an
// entry that doesn't).
import { useState } from 'react'
import { useAction } from '../lib/hooks'
import { kube, save, type Obj } from '../lib/kube'
import { TYPES } from '../lib/types'
import { KeyValueEditor, SelectField, TextField } from './form'
import { Modal } from './Modal'
import { ConfirmButton, EmptyState, ErrorBanner } from './ui'

type Entry = { name: string; issuer: string; audience?: string; claims: Record<string, string> }

const NAME_RE = /^[a-z0-9]([-a-z0-9]*[a-z0-9])?$/

// internal/signin/federation.go's bindingClaims.
const BINDING = ['sub', 'repository', 'repository_owner', 'repository_id', 'repository_owner_id', 'project_path', 'project_id', 'namespace_path', 'namespace_id', 'pipeline_slug', 'organization_slug']

const SYSTEMS = {
  github: { label: 'GitHub Actions', issuer: 'https://token.actions.githubusercontent.com', claim: 'repository', placeholder: 'org/repo' },
  gitlab: { label: 'GitLab CI (gitlab.com)', issuer: 'https://gitlab.com', claim: 'project_path', placeholder: 'group/project' },
  other: { label: 'Another CI system', issuer: '', claim: '', placeholder: '' },
} as const
type System = keyof typeof SYSTEMS

function problem(e: Entry): string {
  if (!NAME_RE.test(e.name)) return "The name is a DNS label: lowercase letters, digits and '-'"
  if (!e.issuer.startsWith('https://')) return 'The issuer is an https:// URL'
  if (!BINDING.some((k) => (e.claims[k] || '').replace(/\*/g, '') !== '')) return `Pin the repository or project with one of: ${BINDING.join(', ')}`
  return ''
}

const systemOf = (issuer: string) => (Object.keys(SYSTEMS) as System[]).find((s) => SYSTEMS[s].issuer === issuer)

function loginLine(user: string, e: Entry) {
  if (systemOf(e.issuer) === 'github') return `hyve login URL --username ${user} --github-actions${e.audience ? ` --audience ${e.audience}` : ''}`
  return `hyve login URL --username ${user} --id-token-env HYVE_ID_TOKEN`
}

export function TrustedCI({ user, onClose }: { user: Obj; onClose: () => void }) {
  const name = user.metadata.name
  const [entries, setEntries] = useState<Entry[]>(user.spec?.trustedCI || [])
  const [system, setSystem] = useState<System>('github')
  const [form, setForm] = useState({ name: '', issuer: '', repo: '', audience: '' })
  const [claims, setClaims] = useState<Record<string, string>>({})
  // Bumped after an add, to clear the claims editor.
  const [added, setAdded] = useState(0)
  const { busy, error, setError, run } = useAction()

  // Fresh, so another change to the user in the meantime isn't undone.
  const write = (next: Entry[]) =>
    run(async () => {
      const fresh = await kube.find(TYPES.USER, undefined, name)
      const spec = { ...fresh.spec, trustedCI: next }
      if (!next.length) delete spec.trustedCI
      await save(TYPES.USER, { ...fresh, spec })
      setEntries(next)
      return true
    })

  async function add() {
    const sys = SYSTEMS[system]
    const e: Entry = {
      name: form.name,
      issuer: system === 'other' ? form.issuer.trim().replace(/\/$/, '') : sys.issuer,
      claims: { ...claims, ...(sys.claim && form.repo.trim() ? { [sys.claim]: form.repo.trim() } : {}) },
      ...(form.audience.trim() ? { audience: form.audience.trim() } : {}),
    }
    const bad = problem(e) || (entries.some((x) => x.name === e.name) ? `${name} already trusts ${e.name}` : '')
    if (bad) {
      setError(bad)
      return
    }
    if (await write([...entries, e])) {
      setForm({ name: '', issuer: '', repo: '', audience: '' })
      setClaims({})
      setAdded((n) => n + 1)
    }
  }

  const sys = SYSTEMS[system]
  return (
    <Modal title={`CI sign-in for ${name}`} onClose={onClose} wide>
      <p className="mb-4 text-sm text-neutral-500">
        A CI job signs in as {name} with the ID token its CI system gives it, when the token matches an entry here: no password or API token is kept in
        the pipeline, and the session lasts an hour. A <code>*</code> in a claim matches anything.
      </p>
      {entries.length === 0 ? (
        <EmptyState>{name} trusts no CI system.</EmptyState>
      ) : (
        <div className="mb-4 divide-y divide-neutral-100 rounded-lg border border-neutral-200 dark:divide-neutral-800 dark:border-neutral-800">
          {entries.map((e) => (
            <div key={e.name} className="flex items-start justify-between gap-3 p-3">
              <div className="min-w-0 text-sm">
                <div className="font-medium text-neutral-900 dark:text-neutral-100">
                  {e.name} <span className="font-normal text-neutral-500">· {SYSTEMS[systemOf(e.issuer) || 'other'].label}</span>
                </div>
                <div className="mt-0.5 text-xs break-all text-neutral-500">
                  {Object.entries(e.claims || {})
                    .map(([k, v]) => `${k}=${v}`)
                    .join(', ')}
                  {e.audience ? ` · audience ${e.audience}` : ''}
                  {systemOf(e.issuer) ? '' : ` · ${e.issuer}`}
                </div>
                <code className="mt-1 block text-xs break-all text-neutral-600 dark:text-neutral-400">{loginLine(name, e)}</code>
                {problem(e) && <p className="mt-1 text-xs text-red-600 dark:text-red-400">Ignored by the host: {problem(e)}</p>}
              </div>
              <ConfirmButton label="Remove" confirm={`Remove ${e.name}?`} onConfirm={() => write(entries.filter((x) => x.name !== e.name))} />
            </div>
          ))}
        </div>
      )}

      <form
        className="border-t border-neutral-100 pt-4 dark:border-neutral-800"
        onSubmit={(ev) => {
          ev.preventDefault()
          add()
        }}
      >
        <h3 className="mb-3 text-sm font-semibold">Trust a CI system's jobs</h3>
        <div className="mb-3 grid gap-3 sm:grid-cols-2">
          <SelectField
            label="CI system"
            value={system}
            onChange={(v) => setSystem(v as System)}
            options={(Object.keys(SYSTEMS) as System[]).map((k) => ({ value: k, label: SYSTEMS[k].label }))}
          />
          <TextField label="Name" value={form.name} onChange={(v) => setForm((f) => ({ ...f, name: v }))} placeholder="web-releases" />
        </div>
        <div className="mb-3 grid gap-3 sm:grid-cols-2">
          {system === 'other' ? (
            <TextField label="Issuer" value={form.issuer} onChange={(v) => setForm((f) => ({ ...f, issuer: v }))} placeholder="https://agent.buildkite.com" />
          ) : (
            <TextField
              label={system === 'github' ? 'Repository' : 'Project'}
              value={form.repo}
              onChange={(v) => setForm((f) => ({ ...f, repo: v }))}
              placeholder={sys.placeholder}
            />
          )}
          <TextField
            label="Audience"
            value={form.audience}
            onChange={(v) => setForm((f) => ({ ...f, audience: v }))}
            placeholder="hyve"
            help="The token must be issued for it."
          />
        </div>
        <div className="mb-3">
          <span className="mb-1 block text-sm font-medium">Other claims</span>
          <KeyValueEditor key={added} value={claims} onChange={setClaims} addLabel="Add a claim" keyPlaceholder="ref" valuePlaceholder="refs/tags/*" />
          <span className="hint">
            {system === 'github' ? 'Such as ref = refs/tags/* or environment = production.' : 'Claims the job token must have; one must pin the project.'}
          </span>
        </div>
        <ErrorBanner error={error} />
        <div className="flex justify-end gap-2">
          <button type="button" className="btn btn-tertiary" onClick={onClose}>
            Close
          </button>
          <button type="submit" className="btn btn-primary" disabled={busy || !form.name}>
            Add
          </button>
        </div>
      </form>
    </Modal>
  )
}
