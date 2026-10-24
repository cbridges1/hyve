// 'hyve import': old hyve Templates and herd ClusterTemplates, pasted or
// loaded from files, converted by the host (the CLI's own code) into hyve
// ClusterTemplates. Creating them is done here, as the signed-in user, and
// for herd templates can label the clusters herd made from them.
import { useState } from 'react'
import { Checkbox } from '../components/form'
import { BackLink, Card, ErrorBanner } from '../components/ui'
import { ViewDialog } from '../components/ViewDialog'
import { applyObjects } from '../lib/apply'
import { host, kube, patch } from '../lib/kube'
import { paths } from '../lib/routes'
import { DEFAULT_TEMPLATE_NAMESPACE, LABELS, TYPES } from '../lib/types'

type Result = { kind: string; source: string; name?: string; template?: any; skipped?: string; notes?: string[]; herd?: { namespace: string; name: string } }
type Done = { object: string; action?: string; error?: string }

export default function ImportPage() {
  const [text, setText] = useState('')
  const [templateNamespace, setTemplateNamespace] = useState(DEFAULT_TEMPLATE_NAMESPACE)
  const [prefixNamespace, setPrefixNamespace] = useState(false)
  const [relabel, setRelabel] = useState(false)
  const [results, setResults] = useState<Result[] | null>(null)
  const [applied, setApplied] = useState<Done[]>([])
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const [viewing, setViewing] = useState<Result | null>(null)

  const importable = (results || []).filter((r) => r.template)
  const anyHerd = importable.some((r) => r.herd)

  async function convert() {
    setError('')
    setResults(null)
    setApplied([])
    setBusy(true)
    try {
      const res = await host('import', { yaml: text, templateNamespace, prefixNamespace })
      setResults(res.results || [])
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  async function create() {
    setError('')
    setBusy(true)
    const out: Done[] = []
    try {
      out.push(...(await applyObjects(importable.map((r) => r.template), { templateNamespace })))
      setApplied([...out])
      if (relabel) await relabelHerd(out)
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setApplied([...out])
      setBusy(false)
    }
  }

  // --relabel-clusters: the clusters herd made from a herd template get
  // the new template's labels, so they get its resources and locks.
  async function relabelHerd(out: Done[]) {
    const clusters = await kube.findAll(TYPES.CLUSTER, { force: true })
    for (const r of importable.filter((x) => x.herd)) {
      const ok = out.find((a) => a.object.endsWith(`/${r.name}`) && a.action)
      if (!ok) continue
      const mine = clusters.filter((c) => c.metadata.namespace === r.herd!.namespace && c.metadata.labels?.['herd.io/template'] === r.herd!.name)
      for (const c of mine) {
        await patch(TYPES.CLUSTER, c.metadata.namespace, c.metadata.name, {
          metadata: { labels: { [LABELS.TEMPLATE]: r.name, [LABELS.TEMPLATE_NAMESPACE]: templateNamespace } },
        })
        out.push({ object: `Cluster ${c.metadata.namespace}/${c.metadata.name}`, action: 'relabelled' })
        setApplied([...out])
      }
    }
  }

  return (
    <div className="space-y-4">
      <div>
        <BackLink to={paths.templates} label="Templates" />
        <h1 className="mb-0">Import templates</h1>
      </div>
      <p className="text-sm text-neutral-500">
        Converts old hyve Templates (hyve-capi-module, hyve-civo-module, hyve-unraid-k3s-module) and herd ClusterTemplates into hyve ClusterTemplates:
        flavor presets expanded, params mapped onto the ClusterClass, locks, the schedule, and Helm and git resources. What hyve can't express is listed
        for each. Also <code>hyve import</code>.
      </p>
      <Card>
        <textarea
          value={text}
          onChange={(e) => setText(e.target.value)}
          rows={14}
          className="font-mono text-xs"
          spellCheck={false}
          aria-label="Old templates as YAML"
          placeholder={'apiVersion: hyve.io/v1alpha1\nkind: Template\n...'}
        />
        <div className="mt-3 flex flex-wrap items-center gap-3">
          <label className="btn btn-tertiary relative overflow-hidden">
            Load files
            <input
              type="file"
              multiple
              accept=".yaml,.yml"
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
          <label className="flex items-center gap-2 text-sm">
            <span className="whitespace-nowrap text-neutral-600 dark:text-neutral-400">Template namespace</span>
            <input value={templateNamespace} onChange={(e) => setTemplateNamespace(e.target.value)} className="w-44!" spellCheck={false} />
          </label>
          <Checkbox checked={prefixNamespace} onChange={setPrefixNamespace} label="Name them <old namespace>-<name>" />
          <span className="flex-1" />
          <button type="button" className="btn btn-primary" disabled={busy || !text.trim()} onClick={convert}>
            Convert
          </button>
        </div>
      </Card>
      <ErrorBanner error={error} />
      {results && (
        <Card title="Result">
          <div className="overflow-x-auto">
            <table className="table">
              <thead>
                <tr>
                  <th>From</th>
                  <th>ClusterTemplate</th>
                  <th>Notes</th>
                  <th />
                </tr>
              </thead>
              <tbody>
                {results.map((r) => (
                  <tr key={r.source}>
                    <td>
                      {r.kind} {r.source}
                    </td>
                    <td>
                      {r.template ? (
                        <span>
                          {templateNamespace}/{r.name}
                        </span>
                      ) : (
                        <span className="text-error">Skipped: {r.skipped}</span>
                      )}
                    </td>
                    <td className="text-xs">
                      {(r.notes || []).map((n) => (
                        <div key={n}>{n}</div>
                      ))}
                      {!(r.notes || []).length && <span>-</span>}
                    </td>
                    <td className="text-right">
                      {r.template && (
                        <button type="button" className="btn btn-tertiary btn-sm" onClick={() => setViewing(r)}>
                          View
                        </button>
                      )}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          {importable.length > 0 && (
            <div className="mt-4 flex flex-wrap items-center gap-3">
              {anyHerd && <Checkbox checked={relabel} onChange={setRelabel} label="Label the clusters herd made from them with the new templates" />}
              <span className="flex-1" />
              <button type="button" className="btn btn-primary" disabled={busy} onClick={create}>
                Create {importable.length} template{importable.length === 1 ? '' : 's'}
              </button>
            </div>
          )}
          {applied.length > 0 && (
            <ul className="mt-3 space-y-1 text-sm">
              {applied.map((a, i) => (
                <li key={i} className={a.error ? 'text-error' : 'text-success'}>
                  {a.object}: {a.error || a.action}
                </li>
              ))}
            </ul>
          )}
        </Card>
      )}
      {viewing && <ViewDialog title={viewing.name || ''} sections={[{ label: 'ClusterTemplate', object: viewing.template }]} onClose={() => setViewing(null)} />}
    </div>
  )
}
