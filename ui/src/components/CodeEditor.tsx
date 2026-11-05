// A YAML (or plain text) editor: CodeMirror, as Rancher's, with line
// numbers, folding (a nested block, such as status or managedFields,
// collapses from the gutter or with Ctrl/Cmd-Alt-[), search
// (Ctrl/Cmd-F) and undo. Drop a file on it, or use Upload, to load one.
//
// It scrolls in itself and ends at the window's bottom (lib/fitHeight.ts),
// or at maxHeight when given (a pane in a page that scrolls).
// fill makes it that tall even when the text is short: 'window' for a page
// that's all editor, 'parent' to fill a flex column (a full-screen view).
import { defaultKeymap, history, historyKeymap, indentWithTab } from '@codemirror/commands'
import { yaml } from '@codemirror/lang-yaml'
import { foldGutter, foldKeymap, indentOnInput, syntaxHighlighting } from '@codemirror/language'
import { highlightSelectionMatches, searchKeymap } from '@codemirror/search'
import { Compartment, EditorState } from '@codemirror/state'
import { drawSelection, EditorView, highlightActiveLine, highlightActiveLineGutter, keymap, lineNumbers } from '@codemirror/view'
import { classHighlighter } from '@lezer/highlight'
import { useEffect, useRef, useState, type DragEvent } from 'react'
import { useFitHeight } from '../lib/fitHeight'

// Colours come from index.css (.cm-* and tok-* classes), so they follow
// the light and dark themes.
const layout = EditorView.theme({
  // As tall as the text, between the host's min and max heights; or, in
  // fill mode, the host's height.
  '&': { height: '100%', minHeight: 'inherit', maxHeight: 'inherit', fontSize: '12px' },
  '&.cm-focused': { outline: 'none' },
  '.cm-scroller': { overflow: 'auto', fontFamily: 'ui-monospace, SFMono-Regular, Menlo, monospace', lineHeight: '1.55' },
  '.cm-content': { padding: '8px 0' },
  '.cm-gutters': { border: 'none' },
  '.cm-foldGutter .cm-gutterElement': { padding: '0 4px', cursor: 'pointer' },
})

export function CodeEditor({
  value,
  onChange,
  language = 'yaml',
  minHeight = '16rem',
  readOnly,
  label,
  upload = false,
  fill,
  maxHeight,
}: {
  value: string
  onChange?: (v: string) => void
  language?: 'yaml' | 'text'
  minHeight?: string
  readOnly?: boolean
  label?: string
  upload?: boolean
  fill?: 'window' | 'parent'
  maxHeight?: string
}) {
  const box = useRef<HTMLDivElement>(null)
  const height = useFitHeight(box, { min: 320 })
  const host = useRef<HTMLDivElement>(null)
  const view = useRef<EditorView | null>(null)
  const [dragOver, setDragOver] = useState(false)
  const file = useRef<HTMLInputElement>(null)
  // The latest onChange, without rebuilding the editor when it changes.
  const changed = useRef(onChange)
  useEffect(() => {
    changed.current = onChange
  })
  const editable = useRef(new Compartment())
  const lang = useRef(new Compartment())

  useEffect(() => {
    const v = new EditorView({
      parent: host.current!,
      state: EditorState.create({
        doc: value,
        extensions: [
          lineNumbers(),
          foldGutter({ openText: '▾', closedText: '▸' }),
          highlightActiveLineGutter(),
          highlightActiveLine(),
          drawSelection(),
          history(),
          indentOnInput(),
          highlightSelectionMatches(),
          syntaxHighlighting(classHighlighter),
          EditorState.tabSize.of(2),
          keymap.of([...defaultKeymap, ...historyKeymap, ...foldKeymap, ...searchKeymap, indentWithTab]),
          lang.current.of(language === 'yaml' ? yaml() : []),
          editable.current.of([EditorState.readOnly.of(!!readOnly), EditorView.editable.of(!readOnly)]),
          layout,
          EditorView.updateListener.of((u) => {
            if (u.docChanged) changed.current?.(u.state.doc.toString())
          }),
        ],
      }),
    })
    view.current = v
    return () => {
      v.destroy()
      view.current = null
    }
    // Built once; the effects below follow the props.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  // A new value from outside (a reload, Upload, a reset): replace the text,
  // unless it's what the editor already has (the user's own typing).
  useEffect(() => {
    const v = view.current
    if (!v || v.state.doc.toString() === value) return
    v.dispatch({ changes: { from: 0, to: v.state.doc.length, insert: value } })
  }, [value])

  useEffect(() => {
    view.current?.dispatch({ effects: editable.current.reconfigure([EditorState.readOnly.of(!!readOnly), EditorView.editable.of(!readOnly)]) })
  }, [readOnly])

  useEffect(() => {
    view.current?.dispatch({ effects: lang.current.reconfigure(language === 'yaml' ? yaml() : []) })
  }, [language])

  async function load(f: File) {
    onChange?.(await f.text())
  }

  const boxStyle = fill === 'window' ? { height, minHeight } : undefined
  const hostStyle = fill ? { minHeight: fill === 'window' ? minHeight : undefined } : { maxHeight: maxHeight ?? height, minHeight }
  return (
    <div className={fill === 'parent' ? 'flex min-h-0 flex-1 flex-col' : ''}>
      {(label || (upload && !readOnly)) && (
        <div className="mb-1 flex items-center justify-between">
          <span className="label mb-0">{label}</span>
          {upload && !readOnly && (
            <>
              <button type="button" className="btn btn-tertiary btn-sm" onClick={() => file.current?.click()}>
                Upload file…
              </button>
              <input
                ref={file}
                type="file"
                accept=".yaml,.yml,.json,.txt"
                className="hidden"
                onChange={(e) => {
                  const f = e.target.files?.[0]
                  if (f) load(f)
                  e.target.value = ''
                }}
              />
            </>
          )}
        </div>
      )}
      <div
        ref={box}
        onDragOver={(e: DragEvent) => {
          if (readOnly) return
          e.preventDefault()
          setDragOver(true)
        }}
        onDragLeave={() => setDragOver(false)}
        onDrop={(e: DragEvent) => {
          if (readOnly) return
          e.preventDefault()
          setDragOver(false)
          const f = e.dataTransfer.files?.[0]
          if (f) load(f)
        }}
        className={`code-editor flex overflow-hidden rounded-lg border ${fill === 'parent' ? 'min-h-0 flex-1' : ''} ${
          dragOver ? 'border-dashed border-neutral-500' : 'border-neutral-300 dark:border-neutral-700'
        }`}
        style={boxStyle}
      >
        <div ref={host} className="min-h-0 min-w-0 flex-1" style={hostStyle} />
      </div>
    </div>
  )
}
