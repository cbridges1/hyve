// One resource entry, every way the CLI can give one ('--helm', '--git',
// '--path', '--ref' and their options): a Helm chart with values, git
// paths, manifests sent inline, or a shared Resource with value
// overrides. Fields the form doesn't show are kept from the entry it
// started from. The parent reads the result through the ref:
// form.current.entry(), which throws with what's wrong.
import { dump, load, loadAll } from 'js-yaml'
import { forwardRef, useImperativeHandle, useState } from 'react'
import { kindOf, validate, type Entry } from '../lib/resources'
import { CodeEditor } from './CodeEditor'
import { Checkbox, SelectField, TextField } from './form'

type Kind = 'helm' | 'git' | 'bundle' | 'ref'

const KIND_LABELS: Record<Kind, string> = { helm: 'Helm chart', git: 'Git repository', bundle: 'Manifests', ref: 'Existing resource' }

function lastSegment(s: string) {
  const t = (s || '').replace(/\/+$/, '')
  return t.slice(t.lastIndexOf('/') + 1)
}

function list(s: string) {
  return (s || '')
    .split(',')
    .map((x) => x.trim())
    .filter(Boolean)
}

function dumpValues(v: unknown) {
  return v && typeof v === 'object' && Object.keys(v).length ? dump(v, { lineWidth: -1 }) : ''
}

export type ResourceFormHandle = { entry: () => Entry }

type Props = {
  // The entry to edit, or null for a new one.
  value?: Entry | null
  // Whether a shared Resource may be referred to (not in a shared Resource
  // itself).
  allowRef?: boolean
  // Shared Resources to offer.
  shared?: { name: string; namespace: string }[]
  // Hide the name field (a shared Resource's name is its object's).
  noName?: boolean
}

export const ResourceForm = forwardRef<ResourceFormHandle, Props>(function ResourceForm({ value, allowRef = true, shared = [], noName = false }, ref) {
  const e = value || {}
  const startKind = ({ HelmOp: 'helm', GitRepo: 'git', Bundle: 'bundle', Ref: 'ref' } as Record<string, Kind>)[kindOf(e)] || 'helm'
  const h = e.helmOp?.helm || {}
  const g = e.gitRepo || {}
  const startSpec = e.helmOp || e.gitRepo || e.bundle || {}

  const [kind, setKindState] = useState<Kind>(startKind)
  const [name, setName] = useState(e.name || '')
  const [namespace, setNamespace] = useState(e.namespace || '')
  const [secrets, setSecrets] = useState((e.secrets || []).join(', '))
  const [correctDrift, setCorrectDrift] = useState(!!startSpec.correctDrift?.enabled)
  const [repo, setRepo] = useState(h.repo || '')
  const [chart, setChart] = useState(h.chart || '')
  const [version, setVersion] = useState(h.version || '')
  const [releaseName, setReleaseName] = useState(h.releaseName || '')
  const [values, setValues] = useState(dumpValues(startKind === 'ref' ? e.values : h.values))
  const [gitRepo, setGitRepo] = useState(g.repo || '')
  const [branch, setBranch] = useState(g.branch || '')
  const [revision, setRevision] = useState(g.revision || '')
  const [paths, setPaths] = useState((g.paths || []).join(', '))
  const [files, setFiles] = useState<{ name: string; content: string }[]>(() => {
    const f = (e.bundle?.resources || []).map((r: any) => ({ name: r.name || '', content: r.content || '' }))
    return startKind === 'bundle' && !f.length ? [{ name: 'manifests.yaml', content: '' }] : f
  })
  const [refName, setRefName] = useState(e.ref ? `${e.refNamespace ? `${e.refNamespace}/` : ''}${e.ref}` : '')

  const setKind = (k: Kind) => {
    setKindState(k)
    if (k === 'bundle' && !files.length) setFiles([{ name: 'manifests.yaml', content: '' }])
  }
  const addFile = () => setFiles((fs) => [...fs, { name: fs.length ? `manifest-${fs.length + 1}.yaml` : 'manifests.yaml', content: '' }])

  const namePlaceholder = (() => {
    switch (kind) {
      case 'helm':
        return lastSegment(chart || repo)
      case 'git':
        return lastSegment(gitRepo).replace(/\.git$/, '').toLowerCase()
      case 'ref':
        return refName
      default:
        return ''
    }
  })()

  useImperativeHandle(ref, () => ({
    entry() {
      const orig: Entry = JSON.parse(JSON.stringify(value || {}))
      const out: Entry = { name: name.trim() || namePlaceholder }
      const parseValues = () => {
        if (!values.trim()) return undefined
        const v = load(values)
        if (!v || typeof v !== 'object' || Array.isArray(v)) throw new Error('Helm values must be a YAML map.')
        return v
      }
      const drift = (spec: any) => {
        if (correctDrift) spec.correctDrift = { ...(spec.correctDrift || {}), enabled: true }
        else if (spec.correctDrift) {
          delete spec.correctDrift.enabled
          if (!Object.keys(spec.correctDrift).length) delete spec.correctDrift
        }
        return spec
      }
      if (namespace.trim()) out.namespace = namespace.trim()
      const secretList = list(secrets)
      if (secretList.length) out.secrets = secretList

      switch (kind) {
        case 'helm': {
          const spec = { ...(orig.helmOp || {}) }
          const helm = { ...(spec.helm || {}) }
          let r = repo.trim()
          let c = chart.trim()
          // Fleet wants an OCI chart as the whole reference in repo.
          if (c.startsWith('oci://')) {
            r = c
            c = ''
          } else if (r.startsWith('oci://') && c) {
            r = `${r.replace(/\/+$/, '')}/${c}`
            c = ''
          }
          const set = (k: string, v: unknown) => {
            if (v) helm[k] = v
            else delete helm[k]
          }
          set('repo', r)
          set('chart', c)
          set('version', version.trim())
          set('releaseName', releaseName.trim())
          set('values', parseValues())
          spec.helm = helm
          out.helmOp = drift(spec)
          break
        }
        case 'git': {
          const gr: any = { ...(orig.gitRepo || {}), repo: gitRepo.trim() }
          for (const [k, v] of [
            ['branch', branch],
            ['revision', revision],
          ] as const) {
            if (v.trim()) gr[k] = v.trim()
            else delete gr[k]
          }
          const p = list(paths)
          if (p.length) gr.paths = p
          else delete gr.paths
          out.gitRepo = drift(gr)
          break
        }
        case 'bundle': {
          const fs = files.filter((f) => f.content.trim())
          if (!fs.length) throw new Error('Give at least one manifest.')
          for (const f of fs) {
            try {
              loadAll(f.content)
            } catch (err) {
              throw new Error(`${f.name}: ${(err as Error).message}`)
            }
          }
          out.bundle = drift({ ...(orig.bundle || {}), resources: fs.map((f) => ({ name: f.name || 'manifests.yaml', content: f.content })) })
          if (!out.name)
            out.name = (fs[0].name || '')
              .replace(/\.(ya?ml|json)$/i, '')
              .toLowerCase()
              .replace(/[^-a-z0-9]/g, '-')
          break
        }
        case 'ref': {
          let rf = refName.trim()
          if (rf.includes('/')) {
            out.refNamespace = rf.slice(0, rf.indexOf('/'))
            rf = rf.slice(rf.indexOf('/') + 1)
          } else if (orig.refNamespace) out.refNamespace = orig.refNamespace
          out.ref = rf
          if (!out.name) out.name = rf
          const v = parseValues()
          if (v) out.values = v
          break
        }
      }
      if (noName) delete out.name
      const bad = noName ? validate({ ...out, name: 'x' }) : validate(out)
      if (bad) throw new Error(bad)
      return out
    },
  }))

  const kinds = (Object.keys(KIND_LABELS) as Kind[]).filter((k) => k !== 'ref' || allowRef).map((k) => ({ value: k, label: KIND_LABELS[k] }))

  return (
    <div className="space-y-3">
      <div className="grid gap-3 sm:grid-cols-3">
        <SelectField label="Kind" value={kind} onChange={(v) => setKind(v as Kind)} options={kinds} />
        {!noName && <TextField label="Name" value={name} onChange={setName} placeholder={namePlaceholder} />}
        <TextField label="Target namespace" value={namespace} onChange={setNamespace} placeholder="default: the chart's or manifests' own" />
      </div>

      {kind === 'helm' && (
        <div className="grid gap-3 sm:grid-cols-2">
          <TextField label="Helm repository URL" value={repo} onChange={setRepo} placeholder="https://charts.example.com, or oci://..." />
          <TextField label="Chart" value={chart} onChange={setChart} required={!repo.startsWith('oci://')} />
          <TextField label="Version" value={version} onChange={setVersion} />
          <TextField label="Release name" value={releaseName} onChange={setReleaseName} placeholder={name || namePlaceholder} />
        </div>
      )}

      {kind === 'git' && (
        <>
          <TextField label="Git repository URL" value={gitRepo} onChange={setGitRepo} required />
          <div className="grid gap-3 sm:grid-cols-4">
            <TextField label="Branch" value={branch} onChange={setBranch} />
            <TextField label="Revision (commit or tag)" value={revision} onChange={setRevision} />
            <TextField className="sm:col-span-2" label="Paths (comma-separated)" value={paths} onChange={setPaths} />
          </div>
        </>
      )}

      {kind === 'bundle' && (
        <>
          <p className="text-muted text-sm">Kubernetes manifests, sent to clusters as they are. One file can hold several documents separated by ---.</p>
          {files.map((f, i) => (
            <div key={i}>
              <div className="mb-1 flex items-center gap-2">
                <input
                  aria-label="File name"
                  value={f.name}
                  onChange={(ev) => setFiles(files.map((x, j) => (j === i ? { ...x, name: ev.target.value } : x)))}
                />
                <button type="button" className="btn btn-tertiary btn-sm" onClick={() => setFiles(files.filter((_, j) => j !== i))}>
                  Remove
                </button>
              </div>
              <CodeEditor
                value={f.content}
                minHeight="10rem"
                onChange={(v) => setFiles((fs) => fs.map((x, j) => (j === i ? { ...x, content: v } : x)))}
              />
            </div>
          ))}
          <div className="flex gap-2">
            <button type="button" className="btn btn-tertiary btn-sm" onClick={addFile}>
              Add file
            </button>
            <label className="btn btn-tertiary btn-sm relative overflow-hidden">
              Load files
              <input
                type="file"
                multiple
                accept=".yaml,.yml,.json"
                className="absolute inset-0 cursor-pointer opacity-0"
                onChange={async (ev) => {
                  const picked = [...(ev.target.files || [])]
                  ev.target.value = ''
                  const loaded = await Promise.all(picked.map(async (f) => ({ name: f.name, content: await f.text() })))
                  setFiles((fs) => {
                    const next = [...fs]
                    for (const item of loaded) {
                      const empty = next.findIndex((x) => !x.content)
                      if (empty >= 0) next.splice(empty, 1, item)
                      else next.push(item)
                    }
                    return next
                  })
                }}
              />
            </label>
          </div>
        </>
      )}

      {kind === 'ref' && (
        <TextField
          label="Existing resource"
          value={refName}
          onChange={setRefName}
          required
          list={shared.map((r) => r.name)}
          placeholder={shared.length ? `e.g. ${shared[0].name}, or namespace/name` : 'name, or namespace/name'}
        />
      )}

      {(kind === 'helm' || kind === 'ref') && (
        <CodeEditor label={kind === 'ref' ? 'Helm value overrides (YAML)' : 'Helm values (YAML)'} value={values} onChange={setValues} minHeight="7.5rem" />
      )}

      <div className="grid items-end gap-3 sm:grid-cols-3">
        {kind !== 'git' && (
          <TextField
            className="sm:col-span-2"
            label="Secrets to copy"
            value={secrets}
            onChange={setSecrets}
            placeholder="comma-separated Secret names in this namespace"
          />
        )}
        {kind !== 'ref' && (
          <div className="pb-2">
            <Checkbox checked={correctDrift} onChange={setCorrectDrift} label="Undo changes made on clusters" />
          </div>
        )}
      </div>
    </div>
  )
})
