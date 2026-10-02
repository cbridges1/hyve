import { useState } from 'react'
import { dump as dumpYaml, load as loadYaml } from 'js-yaml'
import EditorImport from 'react-simple-code-editor'
import Prism from 'prismjs'
import 'prismjs/components/prism-yaml'
import { Card } from './Card'
import { ApiError } from '../lib/api/client'

// react-simple-code-editor's CJS build (lib/index.js) does `exports.default
// = Editor` with no `__esModule` interop marker — under Vite/esbuild's
// default CJS interop (no marker = "treat the whole exports object as the
// default"), a plain `import Editor from 'react-simple-code-editor'`
// resolves Editor to the *exports object* itself, not the component
// function, and React throws "Element type is invalid ... but got: object"
// the moment it tries to render it. Unwrap defensively so this works
// whichever way a given bundler happens to interop it.
const Editor = (EditorImport as unknown as { default?: typeof EditorImport }).default ?? EditorImport

function highlightYaml(code: string) {
  return Prism.highlight(code, Prism.languages.yaml, 'yaml')
}

// Shared with the line-number gutter below so its rows line up exactly
// with the editor's own text lines — font/size/line-height/top-padding
// all have to match precisely, since they're two independent elements
// rather than one gutter+editor widget.
const MONO_FONT = 'ui-monospace, SFMono-Regular, Menlo, Consolas, monospace'
const EDITOR_FONT_SIZE = '12px'
const EDITOR_LINE_HEIGHT = '18px'
const EDITOR_PADDING = 12

// Plain numbered gutter, not part of react-simple-code-editor itself (it
// doesn't have one) — a same-height sibling column inside the shared
// scroll container below rather than its own scrollable element, so it
// tracks the editor's scroll position for free instead of needing to be
// synced by hand.
function LineNumbers({ lineCount }: { lineCount: number }) {
  return (
    <div
      aria-hidden="true"
      className="shrink-0 border-r border-neutral-200 bg-neutral-100/60 pr-2 pl-3 text-right text-neutral-400 select-none dark:border-neutral-800 dark:bg-neutral-900/60 dark:text-neutral-600"
      style={{
        fontFamily: MONO_FONT,
        fontSize: EDITOR_FONT_SIZE,
        lineHeight: EDITOR_LINE_HEIGHT,
        paddingTop: EDITOR_PADDING,
        paddingBottom: EDITOR_PADDING,
      }}
    >
      {Array.from({ length: lineCount }, (_, i) => (
        <div key={i}>{i + 1}</div>
      ))}
    </div>
  )
}

/**
 * Generic "edit this CR's spec as raw YAML" panel — one component reused
 * across every editable detail page (Cluster/Template/Workflow/Resource)
 * rather than a bespoke structured form per type. Mirrors
 * this codebase's own "a CR is just YAML" model — same mental shape as
 * `kubectl edit`, and the same js-yaml load()/dump() pair the "create from
 * YAML" flows (ClustersListPage/TemplatesPage/WorkflowsPage) already use.
 * Syntax-highlighted via react-simple-code-editor + prismjs's YAML grammar
 * (see index.css's .token.* rules) rather than a plain textarea — cheap
 * enough for this small a panel that a full editor dependency (CodeMirror/
 * Monaco) isn't warranted.
 *
 * Collapsed to a single "Edit" button by default so a detail page reading
 * naturally doesn't lead with a wall of YAML — expands into an editor +
 * Save/Cancel on click.
 */
export function SpecEditor<T>({ spec, onSave }: { spec: T; onSave: (spec: T) => Promise<void> }) {
  const [editing, setEditing] = useState(false)
  const [text, setText] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [saving, setSaving] = useState(false)

  function startEditing() {
    setText(dumpYaml(spec))
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
      parsed = (loadYaml(text) ?? {}) as T
    } catch (err) {
      setError(err instanceof Error ? `Invalid YAML: ${err.message}` : 'Invalid YAML')
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
      <div className="flex max-h-96 overflow-auto rounded-lg border border-neutral-300 bg-neutral-50 dark:border-neutral-700 dark:bg-neutral-950">
        <LineNumbers lineCount={text.split('\n').length} />
        <Editor
          value={text}
          onValueChange={setText}
          highlight={highlightYaml}
          padding={EDITOR_PADDING}
          textareaClassName="focus:outline-none"
          style={{
            fontFamily: MONO_FONT,
            fontSize: EDITOR_FONT_SIZE,
            lineHeight: EDITOR_LINE_HEIGHT,
            minHeight: '260px',
          }}
          className="min-w-0 flex-1 text-neutral-800 dark:text-neutral-200"
        />
      </div>
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
