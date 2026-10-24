// Adds a Cluster API provider: 'hyve provider add'. The host runs the
// CLI's own code (the Cluster API Operator, or local mode's manifests).
import { useState, type ChangeEvent } from 'react'
import { useAction } from '../lib/hooks'
import { host } from '../lib/kube'
import { Card, ErrorBanner } from './ui'
import { SelectField, TextField } from './form'

const TYPES = ['infrastructure', 'bootstrap', 'controlPlane', 'core', 'addon', 'ipam', 'runtimeExtension']
const NAME_RE = /^[a-z0-9]([-a-z0-9]*[a-z0-9])?$/

type Var = { key: string; value: string; b64: boolean; bytes: Uint8Array | null; file: string }

function toBase64(bytes: Uint8Array) {
  let bin = ''
  bytes.forEach((b) => {
    bin += String.fromCharCode(b)
  })
  return btoa(bin)
}

export function ProviderAdd({ onClose, onAdded }: { onClose: () => void; onAdded: (message: string) => void }) {
  const [name, setName] = useState('')
  const [type, setType] = useState('')
  const [version, setVersion] = useState('')
  const [namespace, setNamespace] = useState('')
  const [providerName, setProviderName] = useState('')
  const [fetchURL, setFetchURL] = useState('')
  const [vars, setVars] = useState<Var[]>([])
  const act = useAction()

  const setVar = (i: number, p: Partial<Var>) => setVars((vs) => vs.map((v, j) => (j === i ? { ...v, ...p } : v)))

  const pickFile = (i: number, e: ChangeEvent<HTMLInputElement>) => {
    const f = e.target.files?.[0]
    if (!f) return
    const reader = new FileReader()
    reader.onload = () => setVar(i, { bytes: new Uint8Array(reader.result as ArrayBuffer), file: f.name, value: '' })
    reader.readAsArrayBuffer(f)
    e.target.value = ''
  }

  const body = () => {
    const variables: Record<string, string> = {}
    // A file is --variable-b64-file with base64, else --variable-file
    // (its text, without trailing newlines).
    vars
      .filter((v) => v.key)
      .forEach((v) => {
        const bytes = v.bytes || new TextEncoder().encode(v.value)
        variables[v.key] = v.b64 ? toBase64(bytes) : v.bytes ? new TextDecoder().decode(bytes).replace(/\n+$/, '') : v.value
      })
    return {
      name: name.trim(),
      type: type || undefined,
      version: version.trim() || undefined,
      namespace: namespace.trim() || undefined,
      providerName: providerName.trim() || undefined,
      fetchURL: fetchURL.trim() || undefined,
      variables: Object.keys(variables).length ? variables : undefined,
    }
  }

  const submit = () =>
    act.run(async () => {
      const r = body()
      if (!NAME_RE.test(r.name)) throw new Error('Name: lowercase letters, digits and dashes')
      if (r.fetchURL && !/^https?:\/\//.test(r.fetchURL)) throw new Error('The components URL must be http(s).')
      const out = await host<{ message: string }>('providers', r)
      onAdded(out.message)
    })

  return (
    <Card title="Add provider" className="mb-5">
      <div className="grid grid-cols-1 gap-4 sm:grid-cols-3">
        <TextField label="Name" required placeholder="docker, aws, civo..." value={name} onChange={setName} />
        <SelectField
          label="Type"
          value={type}
          onChange={setType}
          placeholder="default (infrastructure)"
          options={TYPES}
        />
        <TextField label="Version" placeholder="default: the latest" value={version} onChange={setVersion} />
        <TextField label="Namespace" placeholder={name ? `${name}-system` : 'NAME-system'} value={namespace} onChange={setNamespace} />
        <TextField label="clusterctl name" placeholder={name} value={providerName} onChange={setProviderName} />
        <TextField label="Components URL" placeholder="https://.../infrastructure-components.yaml" value={fetchURL} onChange={setFetchURL} />
      </div>
      <h3 className="mt-5 mb-1 text-sm font-semibold">Variables</h3>
      <p className="text-muted mb-3 text-xs">
        What the provider's clusterctl configuration reads: credentials and feature flags. hyve keeps them in the Secret NAME-variables.
      </p>
      {vars.map((v, i) => (
        <div key={i} className="mb-2.5 grid grid-cols-1 items-center gap-2 sm:grid-cols-[minmax(140px,1fr)_2fr_auto_auto_auto]">
          <input value={v.key} placeholder="Variable" aria-label="Variable" onChange={(e) => setVar(i, { key: e.target.value })} />
          <input
            type="password"
            autoComplete="new-password"
            value={v.value}
            placeholder={v.file ? `from ${v.file}` : 'Value'}
            aria-label="Value"
            onChange={(e) => setVar(i, { value: e.target.value, bytes: null, file: '' })}
          />
          <label className="flex items-center gap-1 text-xs whitespace-nowrap">
            <input type="checkbox" checked={v.b64} onChange={(e) => setVar(i, { b64: e.target.checked })} />
            base64
          </label>
          <label className="btn btn-tertiary btn-sm relative cursor-pointer overflow-hidden">
            From file
            <input type="file" className="absolute inset-0 cursor-pointer opacity-0" onChange={(e) => pickFile(i, e)} />
          </label>
          <button type="button" className="btn btn-tertiary btn-sm" onClick={() => setVars((vs) => vs.filter((_, j) => j !== i))}>
            Remove
          </button>
        </div>
      ))}
      <button
        type="button"
        className="btn btn-secondary btn-sm mb-5"
        onClick={() => setVars((vs) => [...vs, { key: '', value: '', b64: false, bytes: null, file: '' }])}
      >
        Add variable
      </button>
      <ErrorBanner error={act.error} />
      <div className="flex gap-2.5">
        <button type="button" className="btn btn-primary" disabled={act.busy} onClick={submit}>
          {act.busy ? 'Adding…' : 'Add'}
        </button>
        <button type="button" className="btn btn-secondary" onClick={onClose}>
          Cancel
        </button>
      </div>
    </Card>
  )
}
