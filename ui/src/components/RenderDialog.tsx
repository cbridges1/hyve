// 'hyve render' for a template on this cluster: the Cluster and Fleet
// objects hyve would create for a cluster made from it, without making
// anything. The host renders them with the CLI's code, from the template,
// its ClusterClass and the shared Resources it refers to.
import { dump } from 'js-yaml'
import { useState, type FormEvent } from 'react'
import { useLoad } from '../lib/hooks'
import { host, kube, type Obj } from '../lib/kube'
import { LABELS, TYPES } from '../lib/types'
import { SelectField, TextArea, TextField } from './form'
import { Modal } from './Modal'
import { ErrorBanner } from './ui'
import { ViewDialog, type Section } from './ViewDialog'

// name=value lines, as --set takes them: JSON values are parsed.
function variables(sets: string): Record<string, unknown> {
  const out: Record<string, unknown> = {}
  sets
    .split('\n')
    .map((l) => l.trim())
    .filter(Boolean)
    .forEach((l) => {
      const i = l.indexOf('=')
      if (i < 1) throw new Error(`"${l}": want name=value`)
      const v = l.slice(i + 1)
      try {
        out[l.slice(0, i)] = JSON.parse(v)
      } catch {
        out[l.slice(0, i)] = v
      }
    })
  return out
}

export function RenderDialog({ template, onClose }: { template: Obj; onClose: () => void }) {
  const [name, setName] = useState('example')
  const [env, setEnv] = useState('')
  const [sets, setSets] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const [result, setResult] = useState<{ notes: string[]; sections: Section[] } | null>(null)

  const { data: envs = [] } = useLoad(async (r) => {
    const nss = await r.findAll(TYPES.NAMESPACE).catch(() => [])
    return nss
      .filter((n) => n.metadata.labels?.[LABELS.ENVIRONMENT] === 'true')
      .map((n) => n.metadata.name)
      .sort()
  }, [])
  const chosen = env || (envs.includes('default') ? 'default' : envs[0] || 'default')

  async function render(ev: FormEvent) {
    ev.preventDefault()
    setError('')
    setBusy(true)
    try {
      const t = template
      const ns = t.metadata.namespace
      const docs: Obj[] = [t]
      const cls = t.spec?.clusterClassRef
      if (cls?.name) {
        const c = await kube.maybe(TYPES.CLUSTER_CLASS, cls.namespace || ns, cls.name).catch(() => null)
        if (c) docs.push(c)
      }
      for (const e of (t.spec?.resources || []).filter((x: any) => x.ref)) {
        const r = await kube.maybe(TYPES.RESOURCE, e.refNamespace || ns, e.ref).catch(() => null)
        if (r) docs.push(r)
      }
      const yaml = docs.map((d) => dump(JSON.parse(JSON.stringify(d)), { lineWidth: -1 })).join('---\n')
      const res = await host('render', { yaml, template: t.metadata.name, name: name || 'example', env: chosen, variables: variables(sets) })
      setResult({
        notes: res.notes || [],
        sections: (res.objects || []).map((o: any) => ({ label: `${o.kind} ${o.metadata?.namespace}/${o.metadata?.name}`, object: o })),
      })
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  if (result) {
    return (
      <ViewDialog title={`${name} from ${template.metadata.name}`} sections={result.sections} onClose={onClose}>
        {result.notes.map((n) => (
          <p key={n} className="text-warning mb-2 text-sm">
            {n}
          </p>
        ))}
      </ViewDialog>
    )
  }

  return (
    <Modal title={`Preview a cluster from ${template.metadata.name}`} onClose={onClose}>
      <form className="space-y-3" onSubmit={render}>
        <p className="text-sm text-neutral-500">
          What hyve would create, without creating it. Also <code>hyve render</code>.
        </p>
        <TextField label="Cluster name" value={name} onChange={setName} />
        <SelectField label="Environment" value={chosen} onChange={setEnv} options={envs} />
        <TextArea label="Variables (name=value, one a line)" value={sets} onChange={setSets} rows={3} mono />
        <ErrorBanner error={error} />
        <div className="flex justify-end gap-2">
          <button type="button" className="btn btn-tertiary" onClick={onClose}>
            Cancel
          </button>
          <button type="submit" className="btn btn-primary" disabled={busy}>
            Preview
          </button>
        </div>
      </form>
    </Modal>
  )
}
