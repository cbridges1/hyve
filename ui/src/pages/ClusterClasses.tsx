// ClusterClasses: the cluster shapes templates are built from, each served
// by an infrastructure provider. One is added from its provider's release
// (clusterclass-NAME.yaml beside the components, as clusterctl finds it),
// from a file's URL, or from YAML; the host installs a published one as
// 'hyve provider add --clusterclass' does. One no cluster uses can be
// deleted (its provider templates stay).
import { useState } from 'react'
import { Link } from 'react-router-dom'
import { ApplyYaml } from '../components/ApplyYaml'
import { RadioGroup, SelectField, TextField } from '../components/form'
import { Banner, Card, ConfirmButton, EmptyState, ErrorBanner, Loading, PageHeader } from '../components/ui'
import { useAction, useLoad } from '../lib/hooks'
import { can, host, idOf, remove, type Obj } from '../lib/kube'
import { classesOf, loadProviders, operatorObject, providerObjects, type Provider } from '../lib/providers'
import { paths } from '../lib/routes'
import { DEFAULT_TEMPLATE_NAMESPACE, TYPES } from '../lib/types'

type Row = { c: Obj; provider?: Provider; templates: Obj[]; clusters: Obj[] }

// What the class's clusters' workers are: its machine deployment and
// machine pool classes.
function workersOf(c: Obj): string {
  const w = c.spec?.workers || {}
  const names = (l: any[] = []) => l.map((x) => x.class).join(', ')
  const parts = [
    w.machineDeployments?.length ? `deployments: ${names(w.machineDeployments)}` : '',
    w.machinePools?.length ? `pools: ${names(w.machinePools)}` : '',
  ].filter(Boolean)
  return parts.join('; ') || 'none'
}

export default function ClusterClasses() {
  const { data, error, loading, reload } = useLoad(async (r) => {
    const [classes, providers, objects, templates, clusters] = await Promise.all([
      r.findAll(TYPES.CLUSTER_CLASS, { force: true }).catch(() => [] as Obj[]),
      loadProviders(r),
      providerObjects(r),
      r.findAll(TYPES.TEMPLATE).catch(() => [] as Obj[]),
      r.findAll(TYPES.CLUSTER).catch(() => [] as Obj[]),
    ])
    const infra = providers.filter((p) => p.type === 'infrastructure')
    const served = await Promise.all(infra.map((p) => classesOf(r, p)))
    const rows: Row[] = classes
      .map((c) => {
        const ns = c.metadata.namespace
        return {
          c,
          provider: infra.find((_, i) => served[i].some((x) => idOf(x) === idOf(c))),
          templates: templates.filter((t) => t.spec?.clusterClassRef?.name === c.metadata.name && (t.spec?.clusterClassRef?.namespace || t.metadata.namespace) === ns),
          clusters: clusters.filter((x) => {
            const ref = x.spec?.topology?.classRef
            return ref?.name === c.metadata.name && (ref?.namespace || x.metadata.namespace) === ns
          }),
        }
      })
      .sort((a, b) => idOf(a.c).localeCompare(idOf(b.c)))
    const urls: Record<string, string> = {}
    infra.forEach((p) => {
      const u = operatorObject(objects, p)?.spec?.fetchConfig?.url
      if (u) urls[p.id] = u
    })
    return { rows, infra, urls }
  }, [])
  const [adding, setAdding] = useState(false)
  const [yaml, setYaml] = useState(false)
  const [message, setMessage] = useState('')
  const act = useAction()

  if (loading || !data) return <Loading />
  const { rows, infra, urls } = data

  return (
    <div>
      <PageHeader
        title="ClusterClasses"
        subtitle="The cluster shapes templates are built from, each served by an infrastructure provider. Add one a provider publishes, or your own."
      >
        {can(TYPES.CLUSTER_CLASS, 'create') && !adding && (
          <button
            type="button"
            className="btn btn-primary"
            onClick={() => {
              setAdding(true)
              setMessage('')
            }}
          >
            Add ClusterClass
          </button>
        )}
      </PageHeader>
      {message && <Banner tone="success">{message}</Banner>}
      <ErrorBanner error={error} />
      <ErrorBanner error={act.error} />
      {adding && (
        <AddClass
          providers={infra}
          urls={urls}
          onYaml={() => {
            setAdding(false)
            setYaml(true)
          }}
          onClose={() => setAdding(false)}
          onAdded={(msg) => {
            setAdding(false)
            setMessage(msg)
            reload(true)
          }}
        />
      )}
      {!rows.length ? (
        <EmptyState>No ClusterClasses yet. Add one its provider publishes, or write your own.</EmptyState>
      ) : (
        <div className="overflow-x-auto">
          <table className="table">
            <thead>
              <tr>
                <th>Name</th>
                <th>Namespace</th>
                <th>Provider</th>
                <th>Workers</th>
                <th>Variables</th>
                <th>Used by</th>
                <th />
              </tr>
            </thead>
            <tbody>
              {rows.map(({ c, provider, templates, clusters }) => {
                const vars: string[] = (c.spec?.variables || []).map((v: any) => v.name)
                return (
                  <tr key={idOf(c)}>
                    <td>
                      <Link to={paths.yaml(TYPES.CLUSTER_CLASS, c.metadata.namespace, c.metadata.name)}>{c.metadata.name}</Link>
                    </td>
                    <td>{c.metadata.namespace}</td>
                    <td>{provider ? <Link to={paths.provider(provider.ns, provider.full)}>{provider.name}</Link> : <span className="text-muted">-</span>}</td>
                    <td className="text-sm">{workersOf(c)}</td>
                    <td title={vars.join(', ')}>{vars.length}</td>
                    <td className="text-sm">
                      {templates.map((t, i) => (
                        <span key={t.metadata.name}>
                          {i > 0 && ', '}
                          <Link to={paths.template(t.metadata.namespace!, t.metadata.name)}>{t.metadata.name}</Link>
                        </span>
                      ))}
                      {templates.length > 0 && clusters.length > 0 && '; '}
                      {clusters.length > 0 && `${clusters.length} cluster${clusters.length === 1 ? '' : 's'}`}
                      {!templates.length && !clusters.length && <span className="text-muted">nothing</span>}
                    </td>
                    <td className="text-right">
                      {can(TYPES.CLUSTER_CLASS, 'delete') &&
                        (clusters.length ? (
                          <span className="text-muted text-xs" title="Delete its clusters first">
                            in use
                          </span>
                        ) : (
                          <ConfirmButton
                            label="Delete"
                            confirm={`Delete ClusterClass ${c.metadata.name}?${templates.length ? ` ${templates.length} template(s) build from it and can't make clusters without it.` : ''} Its provider templates stay.`}
                            disabled={act.busy}
                            onConfirm={() =>
                              act.run(async () => {
                                await remove(TYPES.CLUSTER_CLASS, c)
                                await reload(true)
                              })
                            }
                          />
                        ))}
                    </td>
                  </tr>
                )
              })}
            </tbody>
          </table>
        </div>
      )}
      {yaml && (
        <ApplyYaml
          onClose={() => setYaml(false)}
          onApplied={() => {
            reload(true)
          }}
        />
      )}
    </div>
  )
}

const MODES = [
  { value: 'provider', label: "From a provider's release" },
  { value: 'url', label: 'From a URL' },
  { value: 'yaml', label: 'From YAML' },
]

// Adding a ClusterClass: one a provider publishes beside its components
// (named), a file's URL, or YAML (pasted, in a dialog).
function AddClass({
  providers,
  urls,
  onYaml,
  onClose,
  onAdded,
}: {
  providers: Provider[]
  urls: Record<string, string>
  onYaml: () => void
  onClose: () => void
  onAdded: (message: string) => void
}) {
  const [mode, setMode] = useState('provider')
  const [providerId, setProviderId] = useState(providers[0]?.id || '')
  const provider = providers.find((p) => p.id === providerId)
  const [name, setName] = useState('')
  const [fetchURL, setFetchURL] = useState('')
  const [url, setUrl] = useState('')
  const [namespace, setNamespace] = useState(DEFAULT_TEMPLATE_NAMESPACE)
  const act = useAction()

  const components = fetchURL || (provider ? urls[provider.id] || '' : '')
  const submit = () =>
    act.run(async () => {
      let body: Record<string, string>
      if (mode === 'provider') {
        const n = (name || provider?.name || '').trim()
        if (!n) throw new Error('Pick a provider')
        if (!/^https?:\/\//.test(components)) throw new Error("Give the provider's components URL: its ClusterClasses are published beside it.")
        body = { url: n, fetchURL: components, namespace }
      } else {
        if (!/^https?:\/\//.test(url.trim())) throw new Error('The URL must be http(s).')
        body = { url: url.trim(), namespace }
      }
      const out = await host<{ message: string }>('clusterclasses', body)
      onAdded(out.message)
    })

  return (
    <Card title="Add ClusterClass" className="mb-5">
      <RadioGroup value={mode} onChange={setMode} options={MODES} />
      {mode === 'yaml' ? (
        <div className="mt-4 flex gap-2.5">
          <p className="text-muted m-0 flex-1 text-sm">Paste or load a ClusterClass and the provider templates it refers to; they go in the template namespace unless they name another.</p>
          <button type="button" className="btn btn-primary" onClick={onYaml}>
            Open YAML editor
          </button>
          <button type="button" className="btn btn-secondary" onClick={onClose}>
            Cancel
          </button>
        </div>
      ) : (
        <>
          <ErrorBanner error={act.error} />
          <div className="mt-4 grid grid-cols-1 gap-4 sm:grid-cols-3">
            {mode === 'provider' ? (
              <>
                <SelectField
                  label="Provider"
                  value={providerId}
                  onChange={(v) => {
                    setProviderId(v)
                    setFetchURL('')
                  }}
                  options={providers.map((p) => ({ value: p.id, label: p.name }))}
                  placeholder={providers.length ? undefined : 'No infrastructure providers'}
                />
                <TextField label="Class" placeholder={provider?.name || 'NAME'} hint="Published as clusterclass-NAME.yaml beside the components." value={name} onChange={setName} />
                <TextField
                  label="Components URL"
                  placeholder="https://.../infrastructure-components.yaml"
                  hint={provider && urls[provider.id] ? 'Where the provider was installed from.' : "Where the provider's release is: hyve doesn't know for this one."}
                  value={components}
                  onChange={setFetchURL}
                />
              </>
            ) : (
              <TextField className="sm:col-span-2" label="URL" required placeholder="https://.../clusterclass-NAME.yaml" value={url} onChange={setUrl} />
            )}
            <TextField label="Namespace" hint="Where templates build from it." value={namespace} onChange={setNamespace} />
          </div>
          <div className="mt-4 flex gap-2.5">
            <button type="button" className="btn btn-primary" disabled={act.busy} onClick={submit}>
              {act.busy ? 'Adding…' : 'Add'}
            </button>
            <button type="button" className="btn btn-secondary" onClick={onClose}>
              Cancel
            </button>
          </div>
        </>
      )}
    </Card>
  )
}
