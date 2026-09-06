import { useState } from 'react'
import { Card } from './Card'
import { ApiError } from '../lib/api/client'

/**
 * Generic "edit this CR's spec as raw JSON" panel — one component reused
 * across every editable detail page (Cluster/Template/Workflow/Resource/
 * AccessMethod) rather than a bespoke structured form per type. Mirrors
 * this codebase's own "a CR is just YAML" model — same mental shape as
 * `kubectl edit`, just JSON instead of YAML since that's what the API
 * already speaks.
 *
 * Collapsed to a single "Edit" button by default so a detail page reading
 * naturally doesn't lead with a wall of JSON — expands into a textarea +
 * Save/Cancel on click.
 */
export function SpecEditor<T>({ spec, onSave }: { spec: T; onSave: (spec: T) => Promise<void> }) {
  const [editing, setEditing] = useState(false)
  const [text, setText] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [saving, setSaving] = useState(false)

  function startEditing() {
    setText(JSON.stringify(spec, null, 2))
    setError(null)
    setEditing(true)
  }

  function cancel() {
    setEditing(false)
    setError(null)
  }

  async function save() {
    setError(null)
    let parsed: T
    try {
      parsed = JSON.parse(text)
    } catch {
      setError('Not valid JSON — fix the syntax and try again.')
      return
    }
    setSaving(true)
    try {
      await onSave(parsed)
      setEditing(false)
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Failed to save')
    } finally {
      setSaving(false)
    }
  }

  if (!editing) {
    return (
      <Card title="Edit">
        <button
          type="button"
          onClick={startEditing}
          className="rounded-lg border border-neutral-300 px-3 py-1.5 text-sm font-medium text-neutral-700 transition-colors hover:bg-neutral-100 dark:border-neutral-700 dark:text-neutral-300 dark:hover:bg-neutral-800"
        >
          Edit spec
        </button>
      </Card>
    )
  }

  return (
    <Card title="Edit spec">
      <textarea
        value={text}
        onChange={(e) => setText(e.target.value)}
        spellCheck={false}
        rows={16}
        className="w-full rounded-lg border border-neutral-300 bg-neutral-50 p-3 font-mono text-xs text-neutral-800 dark:border-neutral-700 dark:bg-neutral-950 dark:text-neutral-200"
      />
      {error && <p className="mt-2 text-sm text-red-600 dark:text-red-400">{error}</p>}
      <div className="mt-3 flex gap-2">
        <button
          type="button"
          onClick={save}
          disabled={saving}
          className="rounded-lg bg-neutral-900 px-3.5 py-1.5 text-sm font-medium text-white transition-colors hover:bg-neutral-800 disabled:opacity-50 dark:bg-white dark:text-neutral-900 dark:hover:bg-neutral-200"
        >
          {saving ? 'Saving…' : 'Save'}
        </button>
        <button
          type="button"
          onClick={cancel}
          disabled={saving}
          className="rounded-lg px-3.5 py-1.5 text-sm font-medium text-neutral-600 transition-colors hover:bg-neutral-100 disabled:opacity-50 dark:text-neutral-400 dark:hover:bg-neutral-800"
        >
          Cancel
        </button>
      </div>
    </Card>
  )
}
