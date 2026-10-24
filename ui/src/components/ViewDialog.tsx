// A read-only view of one or more objects as YAML, in a dialog: what's
// behind a row on a page.
import { dump } from 'js-yaml'
import { useState, type ReactNode } from 'react'
import { Modal } from './Modal'

// Kubernetes' own fields.
const FIELDS = ['apiVersion', 'kind', 'metadata', 'spec', 'status', 'data', 'stringData', 'type', 'rules', 'subjects', 'roleRef']

// What an object is, as YAML: 'kubectl get -o yaml' without managedFields.
export function toYAML(obj: unknown): string {
  if (!obj) return ''
  const plain = JSON.parse(JSON.stringify(obj))
  const out: Record<string, unknown> = {}
  const keys = plain.apiVersion || plain.metadata ? FIELDS : Object.keys(plain)
  for (const k of keys) if (plain[k] !== undefined) out[k] = plain[k]
  if (out.metadata) delete (out.metadata as any).managedFields
  return dump(out, { lineWidth: -1, noRefs: true })
}

export type Section = { label: string; object: unknown }

export function ViewDialog({ title, sections, onClose, children }: { title: string; sections: Section[]; onClose: () => void; children?: ReactNode }) {
  const [copied, setCopied] = useState(-1)
  const shown = sections.map((s) => ({ label: s.label, text: typeof s.object === 'string' ? s.object : toYAML(s.object) }))
  return (
    <Modal title={title} onClose={onClose} wide>
      {children}
      {shown.map((s, i) => (
        <div key={i} className={i ? 'mt-4' : ''}>
          <div className="mb-1.5 flex items-center justify-between text-xs font-semibold tracking-wider text-neutral-500 uppercase">
            <span>{s.label}</span>
            <button
              type="button"
              className="btn btn-tertiary btn-sm normal-case"
              onClick={async () => {
                try {
                  await navigator.clipboard.writeText(s.text)
                  setCopied(i)
                  setTimeout(() => setCopied(-1), 1500)
                } catch {
                  // No clipboard (an insecure origin); the text is still selectable.
                }
              }}
            >
              {copied === i ? 'Copied' : 'Copy'}
            </button>
          </div>
          <pre className="max-h-[60vh] overflow-auto rounded-lg bg-neutral-50 p-3 font-mono text-xs whitespace-pre text-neutral-700 dark:bg-neutral-950 dark:text-neutral-300">
            {s.text}
          </pre>
        </div>
      ))}
    </Modal>
  )
}
