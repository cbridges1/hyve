import { useRef, useState, type DragEvent } from 'react'
import { dump as dumpYaml, load as loadYaml } from 'js-yaml'

/**
 * A YAML textarea plus a file-upload affordance (button + drag-and-drop) —
 * every "Advanced (YAML)" mode across Clusters/Templates/Workflows/
 * Resources used to be a bare `<textarea>` a caller had to hand-type or
 * paste into; this is the same textarea with an added way to load a local
 * .yaml/.yml file's content into it, so a file already sitting on disk
 * (exported from another install, checked into a repo, written by hand in
 * a real editor) doesn't need to be opened and copy-pasted first.
 *
 * extractSpec (Clusters/Templates/Workflows, not Resources — see each
 * page's own usage): a real-world YAML file for one of hyve's own CRs is
 * almost always the FULL envelope (apiVersion/kind/metadata/spec), the
 * same shape `kubectl apply`/`hyve apply` accepts and this console's own
 * "Advanced (YAML)" mode has never accepted — it only ever posted the
 * *spec* body straight to the create API (see TemplatesPage's own
 * YAML_SPEC_PLACEHOLDER, which starts at `driver:`, not `apiVersion:`).
 * Uploading such a file without unwrapping it would silently send a
 * spec-shaped request with driver/region/params nested one level too deep
 * under a stray top-level `spec:` key. When true, a successfully-parsed
 * upload whose top level has both `metadata`/`spec` keys is unwrapped:
 * `.spec` becomes the textarea's new content (re-serialized, so what's
 * shown always matches what submit() will actually parse), and
 * `.metadata.name`, if present, is reported via onNameDetected so the
 * form's separate Name field can be pre-filled the same way a human
 * reading the file would fill it in by hand. A file that doesn't parse as
 * YAML at all, or parses but isn't a full envelope, loads as-is — the
 * existing "Invalid YAML" error at submit time already covers the
 * genuinely-malformed case, so this never needs a second error path of
 * its own for that.
 */
export function YamlEditor({
  value,
  onChange,
  onNameDetected,
  extractSpec = false,
  rows = 10,
  placeholder,
  label = 'Spec (YAML)',
}: {
  value: string
  onChange: (yaml: string) => void
  onNameDetected?: (name: string) => void
  extractSpec?: boolean
  rows?: number
  placeholder?: string
  label?: string
}) {
  const [dragOver, setDragOver] = useState(false)
  const [fileError, setFileError] = useState<string | null>(null)
  const fileInputRef = useRef<HTMLInputElement>(null)

  async function loadFile(file: File) {
    setFileError(null)
    let text: string
    try {
      text = await file.text()
    } catch (err) {
      setFileError(err instanceof Error ? `Failed to read file: ${err.message}` : 'Failed to read file')
      return
    }

    let toLoad = text
    if (extractSpec) {
      try {
        const parsed = loadYaml(text)
        if (parsed && typeof parsed === 'object' && 'spec' in parsed) {
          const withEnvelope = parsed as { metadata?: { name?: string }; spec?: unknown }
          if (withEnvelope.metadata?.name) onNameDetected?.(withEnvelope.metadata.name)
          toLoad = dumpYaml(withEnvelope.spec ?? {})
        }
      } catch {
        // Not parseable as YAML at all (or not a full envelope) — fall
        // back to the raw file content, same as a direct paste; submit()'s
        // own loadYaml call surfaces a genuinely invalid file's error.
      }
    }
    onChange(toLoad)
  }

  function onFileInputChange(e: React.ChangeEvent<HTMLInputElement>) {
    const file = e.target.files?.[0]
    if (file) loadFile(file)
    e.target.value = '' // allow re-selecting the same file name twice in a row
  }

  function onDrop(e: DragEvent<HTMLTextAreaElement>) {
    e.preventDefault()
    setDragOver(false)
    const file = e.dataTransfer.files?.[0]
    if (file) loadFile(file)
  }

  return (
    <div>
      <div className="mb-1 flex items-center justify-between">
        <span className="block text-sm text-neutral-600 dark:text-neutral-400">{label}</span>
        <button
          type="button"
          onClick={() => fileInputRef.current?.click()}
          className="rounded-lg px-2 py-1 text-xs font-medium text-neutral-600 transition-colors hover:bg-neutral-100 dark:text-neutral-400 dark:hover:bg-neutral-800"
        >
          Upload file…
        </button>
        <input ref={fileInputRef} type="file" accept=".yaml,.yml" onChange={onFileInputChange} className="hidden" />
      </div>
      <textarea
        value={value}
        onChange={(e) => onChange(e.target.value)}
        onDragOver={(e) => {
          e.preventDefault()
          setDragOver(true)
        }}
        onDragLeave={() => setDragOver(false)}
        onDrop={onDrop}
        rows={rows}
        placeholder={placeholder}
        className={`w-full rounded-lg border px-2.5 py-1.5 font-mono text-xs transition-colors dark:bg-neutral-800 ${
          dragOver ? 'border-neutral-500 border-dashed dark:border-neutral-400' : 'border-neutral-300 dark:border-neutral-700'
        }`}
      />
      <span className="mt-1 block text-xs text-neutral-500">Drag a .yaml file onto the box, or use Upload file above.</span>
      {fileError && <span className="mt-1 block text-xs text-red-600 dark:text-red-400">{fileError}</span>}
    </div>
  )
}
