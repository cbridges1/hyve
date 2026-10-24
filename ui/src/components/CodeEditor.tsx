// A YAML (or plain text) editor with highlighting, as the old console's
// spec editor: react-simple-code-editor over prismjs. Drop a file on it, or
// use Upload, to load one.
import { useRef, useState, type DragEvent } from 'react'
import EditorModule from 'react-simple-code-editor'
import Prism from 'prismjs'
import 'prismjs/components/prism-yaml'

// The package is CommonJS: its default export arrives as a module object
// in the production build.
const Editor = ((EditorModule as unknown as { default?: typeof EditorModule }).default ?? EditorModule) as typeof EditorModule

export function CodeEditor({
  value,
  onChange,
  language = 'yaml',
  minHeight = '16rem',
  readOnly,
  label,
  upload = false,
}: {
  value: string
  onChange?: (v: string) => void
  language?: 'yaml' | 'text'
  minHeight?: string
  readOnly?: boolean
  label?: string
  upload?: boolean
}) {
  const [dragOver, setDragOver] = useState(false)
  const file = useRef<HTMLInputElement>(null)
  const highlight = (code: string) =>
    language === 'yaml' ? Prism.highlight(code, Prism.languages.yaml, 'yaml') : code.replace(/[&<>]/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;' })[c]!)

  async function load(f: File) {
    onChange?.(await f.text())
  }

  return (
    <div>
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
        className={`overflow-auto rounded-lg border bg-neutral-50 dark:bg-neutral-950 ${
          dragOver ? 'border-dashed border-neutral-500' : 'border-neutral-300 dark:border-neutral-700'
        }`}
        style={{ maxHeight: '70vh' }}
      >
        <Editor
          value={value}
          onValueChange={(v) => onChange?.(v)}
          highlight={highlight}
          readOnly={readOnly}
          padding={12}
          textareaClassName="!outline-none !ring-0 !border-0 !bg-transparent"
          className="font-mono text-xs text-neutral-800 dark:text-neutral-200"
          style={{ minHeight, fontFamily: 'ui-monospace, SFMono-Regular, Menlo, monospace' }}
        />
      </div>
    </div>
  )
}
