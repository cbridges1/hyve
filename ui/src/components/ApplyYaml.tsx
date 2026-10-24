// Creates or updates ClusterTemplates, shared Resources and
// ClusterResources from YAML: 'hyve template apply -f'. Pasted, or loaded
// from files.
import { useState } from 'react'
import { applyObjects, parseDocs, type Applied } from '../lib/apply'
import { CodeEditor } from './CodeEditor'
import { Modal } from './Modal'
import { ErrorBanner } from './ui'

export function ApplyYaml({ onClose, onApplied }: { onClose: () => void; onApplied?: () => void }) {
  const [text, setText] = useState('')
  const [results, setResults] = useState<Applied[]>([])
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  async function apply() {
    setError('')
    setResults([])
    let docs: any[]
    try {
      docs = parseDocs(text)
    } catch (e) {
      setError((e as Error).message)
      return
    }
    if (!docs.length) {
      setError("There's nothing to apply.")
      return
    }
    setBusy(true)
    try {
      const out = await applyObjects(docs)
      setResults(out)
      if (out.some((r) => r.action)) onApplied?.()
    } finally {
      setBusy(false)
    }
  }

  return (
    <Modal title="Apply YAML" onClose={onClose} wide>
      <p className="text-muted mb-2.5 text-sm">
        Creates or updates ClusterTemplates, shared Resources and ClusterResources (hyve.io), as <code>hyve template apply -f</code> does. Without a
        namespace, templates and Resources go in the template namespace and a ClusterResource in the default environment.
      </p>
      <CodeEditor value={text} onChange={setText} minHeight="20rem" />
      <div className="mt-2.5">
        <ErrorBanner error={error} />
      </div>
      {results.length > 0 && (
        <ul className="mb-2.5 list-disc pl-5 text-sm">
          {results.map((r, i) => (
            <li key={i} className={r.error ? 'text-error' : 'text-success'}>
              {r.object}: {r.error || r.action}
            </li>
          ))}
        </ul>
      )}
      <div className="flex items-center gap-2">
        <label className="btn btn-tertiary relative overflow-hidden">
          Load files
          <input
            type="file"
            multiple
            accept=".yaml,.yml,.json"
            className="absolute inset-0 cursor-pointer opacity-0"
            onChange={(ev) => {
              const files = [...(ev.target.files || [])]
              Promise.all(files.map((f) => f.text())).then((texts) =>
                setText((cur) => [cur.trim(), ...texts.map((t) => t.trim())].filter(Boolean).join('\n---\n')),
              )
              ev.target.value = ''
            }}
          />
        </label>
        <span className="flex-1" />
        <button type="button" className="btn btn-secondary" onClick={onClose}>
          Close
        </button>
        <button type="button" className="btn btn-primary" disabled={busy || !text.trim()} onClick={apply}>
          Apply
        </button>
      </div>
    </Modal>
  )
}
