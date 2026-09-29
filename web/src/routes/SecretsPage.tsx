import { useRef, useState, type CSSProperties } from 'react'
import { Modal } from '../components/Modal'
import { AdminOnly } from '../components/RoleGate'
import { RoleAdmin, RoleSuperadmin } from '../lib/api/auth'
import { ApiError } from '../lib/api/client'
import { secretsApi } from '../lib/api/secrets'
import { useConfirm } from '../lib/confirm'
import { keyFromFileName, parseDotenv, SECRET_KEY_PATTERN } from '../lib/dotenv'
import { useApi } from '../lib/useApi'
import { useWhoami } from '../lib/useWhoami'

// Kubernetes caps a whole Secret at 1 MiB, and every key here shares one.
const MAX_FILE_BYTES = 512 * 1024

const inputClass = 'w-full rounded-lg border border-neutral-300 px-2.5 py-1.5 dark:border-neutral-700 dark:bg-neutral-800'
const primaryButton =
  'rounded-lg bg-neutral-900 px-3.5 py-2 text-sm font-medium text-white transition-colors hover:bg-neutral-800 disabled:opacity-50 dark:bg-white dark:text-neutral-900 dark:hover:bg-neutral-200'
const secondaryButton =
  'rounded-lg px-3 py-2 text-sm text-neutral-600 transition-colors hover:bg-neutral-100 dark:text-neutral-400 dark:hover:bg-neutral-700'
// Masks a textarea the way type="password" masks an input, without losing newlines.
const masked: CSSProperties = { WebkitTextSecurity: 'disc' } as CSSProperties

async function readFile(file: File): Promise<string> {
  if (file.size > MAX_FILE_BYTES) throw new Error(`${file.name} is larger than ${MAX_FILE_BYTES / 1024} KiB`)
  return file.text()
}

function FileButton({ label, onFile }: { label: string; onFile: (file: File) => void }) {
  const ref = useRef<HTMLInputElement>(null)
  return (
    <>
      <input
        ref={ref}
        type="file"
        className="hidden"
        onChange={(e) => {
          const file = e.target.files?.[0]
          if (file) onFile(file)
          e.target.value = ''
        }}
      />
      <button type="button" onClick={() => ref.current?.click()} className={secondaryButton}>
        {label}
      </button>
    </>
  )
}

function SecretFormModal({
  initialKey,
  existing,
  onClose,
  onSaved,
}: {
  initialKey: string
  existing: Set<string>
  onClose: () => void
  onSaved: () => void
}) {
  const editing = initialKey !== ''
  const [key, setKey] = useState(initialKey)
  const [value, setValue] = useState('')
  const [show, setShow] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [submitting, setSubmitting] = useState(false)

  const keyValid = SECRET_KEY_PATTERN.test(key)

  async function onFile(file: File) {
    setError(null)
    try {
      setValue(await readFile(file))
      if (!key) setKey(keyFromFileName(file.name))
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to read file')
    }
  }

  async function submit() {
    setError(null)
    setSubmitting(true)
    try {
      await secretsApi.set(key, value)
      onSaved()
      onClose()
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Failed to set secret')
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <Modal title={editing ? `Update ${initialKey}` : 'Add secret'} onClose={onClose}>
      <label className="mb-3 block text-sm">
        <span className="mb-1 block text-neutral-600 dark:text-neutral-400">Key</span>
        <input
          value={key}
          disabled={editing}
          onChange={(e) => setKey(e.target.value)}
          placeholder="MY_API_TOKEN"
          className={`${inputClass} font-mono disabled:opacity-60`}
        />
        {key && !keyValid && (
          <span className="mt-1 block text-xs text-red-600 dark:text-red-400">
            Letters, digits and underscores only, not starting with a digit.
          </span>
        )}
        {!editing && keyValid && existing.has(key) && (
          <span className="mt-1 block text-xs text-amber-600 dark:text-amber-400">{key} already exists — saving replaces its value.</span>
        )}
      </label>

      <div className="mb-1 flex items-center justify-between text-sm">
        <span className="text-neutral-600 dark:text-neutral-400">Value</span>
        <div className="flex items-center gap-1">
          <button type="button" onClick={() => setShow(!show)} className={secondaryButton}>
            {show ? 'Hide' : 'Show'}
          </button>
          <FileButton label="Load from file" onFile={onFile} />
        </div>
      </div>
      <textarea
        value={value}
        onChange={(e) => setValue(e.target.value)}
        rows={6}
        spellCheck={false}
        autoComplete="off"
        placeholder="Paste a value — multi-line values like kubeconfigs and SSH keys are kept as-is"
        style={show ? undefined : masked}
        className={`${inputClass} mb-1 font-mono text-xs`}
      />
      <p className="mb-3 text-xs text-neutral-500">
        {value.length > 0 ? `${value.length} character${value.length === 1 ? '' : 's'}${value.includes('\n') ? `, ${value.split('\n').length} lines` : ''}` : 'Empty'}
      </p>

      {error && <p className="mb-3 text-sm text-red-600 dark:text-red-400">{error}</p>}
      <div className="flex justify-end gap-2">
        <button type="button" onClick={onClose} className={secondaryButton}>
          Cancel
        </button>
        <button type="button" disabled={!keyValid || submitting} onClick={submit} className={primaryButton}>
          {submitting ? 'Saving…' : 'Save'}
        </button>
      </div>
    </Modal>
  )
}

function ImportModal({ existing, onClose, onSaved }: { existing: Set<string>; onClose: () => void; onSaved: () => void }) {
  const [text, setText] = useState('')
  const [show, setShow] = useState(false)
  const [skipped, setSkipped] = useState<Set<string>>(new Set())
  const [error, setError] = useState<string | null>(null)
  const [submitting, setSubmitting] = useState(false)

  const parsed = parseDotenv(text)
  const selected = parsed.entries.filter((e) => !skipped.has(e.key))
  const overwrites = selected.filter((e) => existing.has(e.key)).length

  function toggle(key: string) {
    const next = new Set(skipped)
    if (next.has(key)) next.delete(key)
    else next.add(key)
    setSkipped(next)
  }

  async function onFile(file: File) {
    setError(null)
    try {
      setText(await readFile(file))
      setSkipped(new Set())
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to read file')
    }
  }

  async function submit() {
    setError(null)
    setSubmitting(true)
    try {
      await secretsApi.setMany(Object.fromEntries(selected.map((e) => [e.key, e.value])))
      onSaved()
      onClose()
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Failed to import secrets')
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <Modal title="Import secrets" onClose={onClose}>
      <div className="mb-1 flex items-center justify-between text-sm">
        <span className="text-neutral-600 dark:text-neutral-400">
          Paste <code className="rounded bg-neutral-100 px-1 dark:bg-neutral-900">.env</code> contents
        </span>
        <div className="flex items-center gap-1">
          <button type="button" onClick={() => setShow(!show)} className={secondaryButton}>
            {show ? 'Hide' : 'Show'}
          </button>
          <FileButton label="Choose file" onFile={onFile} />
        </div>
      </div>
      <textarea
        value={text}
        onChange={(e) => {
          setText(e.target.value)
          setSkipped(new Set())
        }}
        rows={6}
        spellCheck={false}
        autoComplete="off"
        placeholder={'# one per line\nAPI_TOKEN=abc123\nexport REGION=us-east-1\nSSH_KEY="-----BEGIN...\n...-----END..."'}
        style={text && !show ? masked : undefined}
        className={`${inputClass} mb-3 font-mono text-xs`}
      />

      {parsed.entries.length > 0 && (
        <div className="mb-3 max-h-56 overflow-y-auto rounded-lg border border-neutral-200 dark:border-neutral-700">
          {parsed.entries.map((e) => (
            <label key={e.key} className="flex cursor-pointer items-center gap-2 border-b border-neutral-100 px-3 py-1.5 text-sm last:border-b-0 dark:border-neutral-700">
              <input type="checkbox" checked={!skipped.has(e.key)} onChange={() => toggle(e.key)} />
              <span className="flex-1 truncate font-mono text-xs text-neutral-800 dark:text-neutral-200">{e.key}</span>
              <span className="text-xs text-neutral-400">{e.value.length} char{e.value.length === 1 ? '' : 's'}</span>
              {existing.has(e.key) ? (
                <span className="rounded bg-amber-100 px-1.5 py-0.5 text-xs text-amber-700 dark:bg-amber-950/50 dark:text-amber-400">replaces</span>
              ) : (
                <span className="rounded bg-emerald-100 px-1.5 py-0.5 text-xs text-emerald-700 dark:bg-emerald-950/50 dark:text-emerald-400">new</span>
              )}
            </label>
          ))}
        </div>
      )}
      {parsed.errors.length > 0 && (
        <div className="mb-3 text-xs text-red-600 dark:text-red-400">
          Skipping {parsed.errors.length === 1 ? 'line' : 'lines'} {parsed.errors.map((e) => e.line).join(', ')} — not KEY=VALUE with a
          valid key.
        </div>
      )}

      {error && <p className="mb-3 text-sm text-red-600 dark:text-red-400">{error}</p>}
      <div className="flex items-center justify-end gap-2">
        {overwrites > 0 && <span className="mr-auto text-xs text-amber-600 dark:text-amber-400">{overwrites} existing value{overwrites === 1 ? '' : 's'} will be replaced</span>}
        <button type="button" onClick={onClose} className={secondaryButton}>
          Cancel
        </button>
        <button type="button" disabled={selected.length === 0 || submitting} onClick={submit} className={primaryButton}>
          {submitting ? 'Importing…' : selected.length === 0 ? 'Import' : `Import ${selected.length} secret${selected.length === 1 ? '' : 's'}`}
        </button>
      </div>
    </Modal>
  )
}

export function SecretsPage() {
  const { data: who } = useWhoami()
  const isAdmin = who?.role === RoleAdmin || who?.role === RoleSuperadmin
  const confirm = useConfirm()
  const { data: names, loading, error, reload } = useApi(() => secretsApi.listNames())
  const [values, setValues] = useState<Record<string, string> | null>(null)
  const [revealError, setRevealError] = useState<string | null>(null)
  // null: closed; '': new secret; otherwise the key being updated.
  const [editKey, setEditKey] = useState<string | null>(null)
  const [importing, setImporting] = useState(false)

  const existing = new Set(names ?? [])

  async function reveal() {
    setRevealError(null)
    try {
      setValues(await secretsApi.listValues())
    } catch (err) {
      setRevealError(err instanceof ApiError ? err.message : 'Failed to load values')
    }
  }

  function onSaved() {
    reload()
    if (values) reveal()
  }

  async function onUnset(key: string) {
    const ok = await confirm({
      title: `Unset "${key}"?`,
      message: 'Any cluster or workflow currently depending on this secret will lose access to it immediately.',
      confirmLabel: 'Unset',
      danger: true,
    })
    if (!ok) return
    await secretsApi.unset(key)
    onSaved()
  }

  return (
    <div>
      <div className="mb-1 flex items-center justify-between gap-3">
        <h1 className="text-lg font-semibold text-neutral-900 dark:text-neutral-100">Secrets</h1>
        <AdminOnly>
          <div className="flex items-center gap-2">
            {values ? (
              <button type="button" onClick={() => setValues(null)} className={secondaryButton}>
                Hide values
              </button>
            ) : (
              <button type="button" onClick={reveal} className={secondaryButton}>
                Reveal values
              </button>
            )}
            <button type="button" onClick={() => setImporting(true)} className={secondaryButton}>
              Import .env
            </button>
            <button type="button" onClick={() => setEditKey('')} className={primaryButton}>
              Add secret
            </button>
          </div>
        </AdminOnly>
      </div>
      <p className="mb-4 text-sm text-neutral-500">
        Backed by a single shared <code className="rounded bg-neutral-100 px-1 dark:bg-neutral-800">hyve-cli-secrets</code>{' '}
        Kubernetes Secret and passed to every module and workflow as environment variables — key names are visible to any
        authenticated caller, values require the admin role.
      </p>

      {loading && <p className="text-sm text-neutral-500">Loading…</p>}
      {error && <p className="text-sm text-red-600 dark:text-red-400">{error}</p>}
      {revealError && <p className="mb-3 text-sm text-red-600 dark:text-red-400">{revealError}</p>}

      <div className="overflow-hidden rounded-xl border border-neutral-200 bg-white shadow-sm dark:border-neutral-800 dark:bg-neutral-900">
        {names?.length === 0 && <p className="p-6 text-center text-sm text-neutral-500">No secrets set.</p>}
        <div className="divide-y divide-neutral-100 dark:divide-neutral-800">
          {names?.map((key) => (
            <div key={key} className="flex items-center justify-between gap-3 px-4 py-3">
              <span className="shrink-0 font-mono text-xs text-neutral-800 dark:text-neutral-200">{key}</span>
              <div className="flex min-w-0 items-center gap-2">
                {values && <span className="truncate font-mono text-xs text-neutral-500" title={values[key]}>{values[key]}</span>}
                {isAdmin && (
                  <>
                    <button
                      type="button"
                      onClick={() => setEditKey(key)}
                      className="shrink-0 rounded-lg px-2.5 py-1 text-sm font-medium text-neutral-600 transition-colors hover:bg-neutral-100 dark:text-neutral-400 dark:hover:bg-neutral-800"
                    >
                      Update
                    </button>
                    <button
                      type="button"
                      onClick={() => onUnset(key)}
                      className="shrink-0 rounded-lg px-2.5 py-1 text-sm font-medium text-red-600 transition-colors hover:bg-red-50 dark:text-red-400 dark:hover:bg-red-950/40"
                    >
                      Unset
                    </button>
                  </>
                )}
              </div>
            </div>
          ))}
        </div>
      </div>

      {editKey !== null && <SecretFormModal initialKey={editKey} existing={existing} onClose={() => setEditKey(null)} onSaved={onSaved} />}
      {importing && <ImportModal existing={existing} onClose={() => setImporting(false)} onSaved={onSaved} />}
    </div>
  )
}
