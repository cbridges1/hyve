// Creates a Secret, or changes an existing one's keys, labels and values,
// as the signed-in user: 'hyve secrets create' and 'hyve secrets set'.
// Values are hidden while typed unless shown; a file's contents can fill
// one, binary files included.
import { useEffect, useState, type ChangeEvent, type FormEvent } from 'react'
import { kube, newObject, save, type Obj } from '../lib/kube'
import { TYPES } from '../lib/types'
import { KeyValueEditor, TextField } from './form'
import { Modal } from './Modal'
import { Banner } from './ui'

const NAME_RE = /^[a-z0-9]([-a-z0-9.]*[a-z0-9])?$/
const KEY_RE = /^[-._a-zA-Z0-9]+$/
const SECRET_TYPES = ['Opaque', 'kubernetes.io/basic-auth', 'kubernetes.io/ssh-auth', 'kubernetes.io/tls', 'kubernetes.io/dockerconfigjson']

type Row = { key: string; value: string; binary: string | null; size: number; shown: boolean }

function toBase64(bytes: Uint8Array) {
  let bin = ''
  bytes.forEach((b) => {
    bin += String.fromCharCode(b)
  })
  return btoa(bin)
}

function fromBase64(b64: string) {
  const bin = atob(b64 || '')
  return Uint8Array.from(bin, (c) => c.charCodeAt(0))
}

// Text when the bytes are UTF-8, else null (a binary value).
function asText(bytes: Uint8Array) {
  try {
    return new TextDecoder('utf-8', { fatal: true }).decode(bytes)
  } catch {
    return null
  }
}

const blankRow = (): Row => ({ key: '', value: '', binary: null, size: 0, shown: true })

export function SecretEdit({
  namespace = '',
  name = '',
  existing = false,
  namespaces = [],
  templateNamespaces = [],
  onClose,
  onSaved,
}: {
  // An existing Secret to change, when existing; else what to start a new one with.
  namespace?: string
  name?: string
  existing?: boolean
  // Namespaces to offer for a new Secret.
  namespaces?: string[]
  // Which of them hold templates; the rest are environments.
  templateNamespaces?: string[]
  onClose: () => void
  onSaved: (s: { namespace: string; name: string }) => void
}) {
  const [secret, setSecret] = useState<Obj | null>(null)
  const [pending, setPending] = useState(existing)
  const [ns, setNs] = useState(namespace || namespaces[0] || '')
  const [nm, setNm] = useState(name)
  const [typ, setTyp] = useState('Opaque')
  const [labels, setLabels] = useState<Record<string, string>>({})
  const [rows, setRows] = useState<Row[]>(existing ? [] : [blankRow()])
  const [errors, setErrors] = useState<string[]>([])
  const [saving, setSaving] = useState(false)

  useEffect(() => {
    if (!existing) return
    let gone = false
    kube
      .find(TYPES.SECRET, namespace, name)
      .then((s) => {
        if (gone) return
        setSecret(s)
        setTyp(s.type || 'Opaque')
        setLabels({ ...(s.metadata?.labels || {}) })
        setRows(
          Object.keys(s.data || {})
            .sort()
            .map((k) => {
              const bytes = fromBase64(s.data![k])
              const text = asText(bytes)
              return { key: k, value: text ?? '', binary: text === null ? s.data![k] : null, size: bytes.length, shown: false }
            }),
        )
      })
      .catch((e) => !gone && setErrors([e instanceof Error ? e.message : String(e)]))
      .finally(() => !gone && setPending(false))
    return () => {
      gone = true
    }
  }, [existing, namespace, name])

  // What the chosen namespace means for who can read the Secret.
  const nsHint = !ns
    ? ''
    : templateNamespaces.includes(ns)
      ? `${ns} is a template namespace: templates' resources can copy this Secret to clusters. Cluster API providers don't read credentials from here; put those in the environment.`
      : `${ns} is an environment: providers read credentials here for clusters created in it, and a cluster's extra resources can copy it. Templates' resources can't copy it from here.`
  const title = existing ? `Edit Secret ${namespace}/${name}` : 'Create a Secret'

  const setRow = (i: number, patch: Partial<Row>) => setRows((rs) => rs.map((r, j) => (j === i ? { ...r, ...patch } : r)))

  const pickFile = (i: number, e: ChangeEvent<HTMLInputElement>) => {
    const f = e.target.files?.[0]
    if (!f) return
    const reader = new FileReader()
    reader.onload = () => {
      const bytes = new Uint8Array(reader.result as ArrayBuffer)
      const text = asText(bytes)
      setRows((rs) =>
        rs.map((r, j) =>
          j !== i
            ? r
            : {
                ...r,
                key: r.key || f.name.replace(/[^-._a-zA-Z0-9]/g, '_'),
                size: bytes.length,
                binary: text === null ? toBase64(bytes) : null,
                value: text === null ? '' : text,
              },
        ),
      )
    }
    reader.readAsArrayBuffer(f)
    e.target.value = ''
  }

  const validate = () => {
    const out: string[] = []
    if (!existing) {
      if (!NAME_RE.test(nm) || nm.length > 253) out.push("Name: lowercase letters, digits, '-' and '.', starting and ending with a letter or digit")
      if (!ns) out.push('Pick a namespace.')
    }
    const seen = new Set<string>()
    rows.forEach((r) => {
      if (!KEY_RE.test(r.key) || r.key === '.' || r.key === '..') out.push(`Key ${r.key || '""'}: letters, digits, '-', '_' and '.'`)
      else if (seen.has(r.key)) out.push(`Key ${r.key} is given twice.`)
      seen.add(r.key)
    })
    if (!rows.length && !existing) out.push('Give at least one key.')
    return out
  }

  const onSubmit = async (ev: FormEvent) => {
    ev.preventDefault()
    const problems = validate()
    setErrors(problems)
    if (problems.length) return
    const data: Record<string, string> = {}
    rows.forEach((r) => {
      data[r.key] = r.binary ?? toBase64(new TextEncoder().encode(r.value))
    })
    const lbls = Object.keys(labels).length ? labels : undefined
    setSaving(true)
    try {
      const s: Obj = secret
        ? { ...secret, metadata: { ...secret.metadata, labels: lbls }, data }
        : newObject(TYPES.SECRET, { metadata: { name: nm, namespace: ns, labels: lbls }, type: typ || 'Opaque', data })
      delete s.stringData
      const out = await save(TYPES.SECRET, s)
      onSaved({ namespace: out.metadata.namespace!, name: out.metadata.name })
    } catch (e) {
      setErrors([e instanceof Error ? e.message : String(e)])
    } finally {
      setSaving(false)
    }
  }

  return (
    <Modal title={title} onClose={onClose} wide>
      <form onSubmit={onSubmit}>
        {pending ? (
          <p className="mb-2.5 text-sm">Loading…</p>
        ) : (
          <>
            {errors.map((e, i) => (
              <Banner key={i} tone="error">
                {e}
              </Banner>
            ))}
            {!existing && (
              <div className="mb-2.5 grid gap-3 sm:grid-cols-3">
                <TextField label="Name" required value={nm} onChange={setNm} />
                <TextField label="Namespace" required value={ns} onChange={setNs} list={namespaces} />
                <TextField label="Type" value={typ} onChange={setTyp} list={SECRET_TYPES} />
              </div>
            )}
            {!existing && nsHint ? (
              <Banner tone="info">{nsHint}</Banner>
            ) : (
              <div className="text-muted mb-2.5 text-sm">Type: {typ}</div>
            )}

            <h3 className="mt-3 mb-2 text-sm font-semibold">Keys</h3>
            {rows.map((r, i) => (
              <div key={i} className="mb-2 grid grid-cols-1 items-start gap-2 sm:grid-cols-[minmax(120px,1fr)_2fr_auto]">
                <input value={r.key} placeholder="Key" aria-label="Key" spellCheck={false} onChange={(e) => setRow(i, { key: e.target.value })} />
                <div>
                  {r.binary !== null ? (
                    <div className="text-muted flex h-9 items-center text-sm">Binary file ({r.size} bytes)</div>
                  ) : r.shown ? (
                    <textarea
                      rows={2}
                      value={r.value}
                      placeholder="Value"
                      aria-label="Value"
                      spellCheck={false}
                      className="min-h-9 font-mono text-xs"
                      onChange={(e) => setRow(i, { value: e.target.value })}
                    />
                  ) : (
                    <input
                      type="password"
                      autoComplete="new-password"
                      value={r.value}
                      placeholder="Value"
                      aria-label="Value"
                      onChange={(e) => setRow(i, { value: e.target.value })}
                    />
                  )}
                </div>
                <div className="flex min-h-9 items-center gap-1">
                  {r.binary === null && (
                    <button type="button" className="btn btn-tertiary btn-sm" onClick={() => setRow(i, { shown: !r.shown })}>
                      {r.shown ? 'Hide' : 'Show'}
                    </button>
                  )}
                  <label className="btn btn-tertiary btn-sm relative cursor-pointer overflow-hidden">
                    From file
                    <input type="file" className="absolute inset-0 cursor-pointer opacity-0" onChange={(e) => pickFile(i, e)} />
                  </label>
                  <button type="button" className="btn btn-tertiary btn-sm" onClick={() => setRows((rs) => rs.filter((_, j) => j !== i))}>
                    Remove
                  </button>
                </div>
              </div>
            ))}
            <button type="button" className="btn btn-secondary btn-sm mb-5" onClick={() => setRows((rs) => [...rs, blankRow()])}>
              Add key
            </button>

            <h3 className="mt-3 mb-2 text-sm font-semibold">Labels</h3>
            <KeyValueEditor value={labels} onChange={setLabels} addLabel="Add label" />

            <div className="mt-5 flex justify-end gap-2">
              <button type="button" className="btn btn-secondary" onClick={onClose}>
                Cancel
              </button>
              <button type="submit" className="btn btn-primary" disabled={saving}>
                {existing ? 'Save' : 'Create'}
              </button>
            </div>
          </>
        )}
      </form>
    </Modal>
  )
}
