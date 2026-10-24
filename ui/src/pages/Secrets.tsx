// The Secrets in the template and environment namespaces, and the ones
// each resource copies to clusters (whether they exist comes from
// hyve-controller's status, for namespaces the user can't list). They can
// be created, changed and deleted here, as the signed-in user: 'hyve
// secrets create/set/delete'.
import { useState } from 'react'
import { EnvironmentPicker } from '../components/EnvironmentPicker'
import { SecretEdit } from '../components/SecretEdit'
import { SecretView } from '../components/SecretView'
import { Banner, ErrorBanner, Loading, PageHeader } from '../components/ui'
import { useEnvironment } from '../lib/environment'
import { useLoad } from '../lib/hooks'
import { idOf, remove, type Obj, type Reader } from '../lib/kube'
import type { Entry } from '../lib/resources'
import { DEFAULT_TEMPLATE_NAMESPACE, LABELS, TYPES } from '../lib/types'

type Row = { key: string; ns: string; secret: string; neededBy: string[]; missing: boolean; exists: boolean; type: string; keys: string[]; clusters: Obj[] }

// Secrets that aren't hyve's to show: service account tokens, Helm's
// release records, and Cluster API's own per-cluster Secrets.
function hidden(s: Obj) {
  const t: string = s.type || ''
  return (
    t === 'kubernetes.io/service-account-token' ||
    t.startsWith('helm.sh/') ||
    !!s.metadata?.labels?.['cluster.x-k8s.io/cluster-name'] ||
    !!(s.metadata?.ownerReferences || []).length
  )
}

async function load(r: Reader) {
  const opt = { force: true }
  const [templates, clusterResources, shared, clusters, nss] = await Promise.all([
    r.findAll(TYPES.TEMPLATE, opt).catch(() => [] as Obj[]),
    r.findAll(TYPES.CLUSTER_RESOURCE, opt).catch(() => [] as Obj[]),
    r.findAll(TYPES.RESOURCE, opt).catch(() => [] as Obj[]),
    r.findAll(TYPES.CLUSTER, opt).catch(() => [] as Obj[]),
    r.findAll(TYPES.NAMESPACE, opt).catch(() => [] as Obj[]),
  ])
  const envs = nss.filter((n) => n.metadata?.labels?.[LABELS.ENVIRONMENT] === 'true').map((n) => n.metadata.name)
  const templateNamespaces = [
    ...new Set([DEFAULT_TEMPLATE_NAMESPACE, ...templates.map((t) => t.metadata.namespace!), ...shared.map((x) => x.metadata.namespace!)]),
  ]
  const namespaces = [...new Set([...templateNamespaces, ...envs])].sort()
  // Per namespace, so no other Secret is read.
  const lists = await Promise.all(namespaces.map((ns) => r.findAll(TYPES.SECRET, { namespace: ns, force: true }).catch(() => null)))
  const listed = new Set<string>()
  const rows: Record<string, Omit<Row, 'clusters'> & { clusters: Map<string, Obj> }> = {}
  const row = (ns: string, secret: string) => {
    const key = `${ns}/${secret}`
    return (rows[key] ||= { key, ns, secret, neededBy: [], missing: false, exists: false, type: '', keys: [], clusters: new Map() })
  }

  lists.forEach((list, i) => {
    if (!list) return
    listed.add(namespaces[i])
    list
      .filter((s) => !hidden(s))
      .forEach((s) => {
        const x = row(s.metadata.namespace!, s.metadata.name)
        x.exists = true
        x.type = s.type || 'Opaque'
        x.keys = Object.keys(s.data || {}).sort()
      })
  })
  const add = (ns: string, secret: string, owner: string, resource: string, missing: boolean, reach: Obj[]) => {
    const x = row(ns, secret)
    x.neededBy.push(`${owner} › ${resource}`)
    // What the listing saw wins; else hyve-controller's word.
    if (!listed.has(ns)) {
      x.missing = x.missing || missing
      x.exists = !x.missing
    }
    reach.forEach((c) => x.clusters.set(idOf(c), c))
  }
  // A template entry's Secrets: its own, or its shared Resource's.
  const entrySecrets = (e: Entry, ns: string): string[] => {
    if (e.ref) {
      const res = shared.find((x) => x.metadata.name === e.ref && x.metadata.namespace === (e.refNamespace || ns))
      return res?.spec?.secrets || []
    }
    return e.secrets || []
  }

  templates.forEach((t) => {
    const ns = t.metadata.namespace!
    const missing: string[] = t.status?.missingSecrets || []
    const reach = clusters.filter(
      (c) => c.metadata?.labels?.[LABELS.TEMPLATE] === t.metadata.name && (c.metadata.labels[LABELS.TEMPLATE_NAMESPACE] || c.metadata.namespace) === ns,
    )
    ;(t.spec?.resources || []).forEach((e: Entry) => {
      entrySecrets(e, ns).forEach((s) => add(ns, s, t.metadata.name, e.name || e.ref || '', missing.includes(s), reach))
    })
  })
  clusterResources.forEach((cr) => {
    const ns = cr.metadata.namespace!
    const missing: string[] = cr.status?.missingSecrets || []
    const reach = clusters.filter((c) => c.metadata.namespace === ns && c.metadata.name === cr.spec?.clusterName)
    const name = cr.metadata?.labels?.[LABELS.RESOURCE] || cr.metadata.name
    entrySecrets(cr.spec || {}, ns).forEach((s) => add(ns, s, cr.spec?.clusterName || cr.metadata.name, name, missing.includes(s), reach))
  })

  return {
    templateNamespaces,
    namespaces,
    rows: Object.values(rows)
      .map((x): Row => ({ ...x, missing: !x.exists, clusters: [...x.clusters.values()] }))
      .sort((a, b) => a.key.localeCompare(b.key)),
  }
}

export default function Secrets() {
  const [environment] = useEnvironment()
  const { data, error, loading, reload } = useLoad(load, [])
  const [viewing, setViewing] = useState<Row | null>(null)
  const [editing, setEditing] = useState<{ existing: boolean; namespace: string; name: string } | null>(null)
  const [confirming, setConfirming] = useState('')
  const [errors, setErrors] = useState<string[]>([])

  if (loading) return <Loading />

  const rows = data?.rows || []
  const tplNS = data?.templateNamespaces || []
  const shown = !environment
    ? rows
    : rows
        .map((r) => ({ ...r, clusters: r.clusters.filter((c) => c.metadata.namespace === environment) }))
        .filter((r) => r.clusters.length || r.ns === environment || tplNS.includes(r.ns))

  const onRemove = async (r: Row) => {
    setErrors([])
    setConfirming('')
    try {
      await remove(TYPES.SECRET, { metadata: { namespace: r.ns, name: r.secret } })
      await reload(true)
    } catch (e) {
      setErrors([e instanceof Error ? e.message : String(e)])
    }
  }

  return (
    <div>
      <PageHeader
        title="Secrets"
        subtitle="Secrets in the template and environment namespaces, and the ones resources copy to clusters. Values stay hidden until you reveal them. Clusters get a change the next time Fleet copies the Secret."
      >
        <EnvironmentPicker />
        <button type="button" className="btn btn-primary" onClick={() => setEditing({ existing: false, namespace: environment || '', name: '' })}>
          Create Secret
        </button>
      </PageHeader>
      <div className="mb-5 text-sm">
        <strong>Which namespace?</strong>
        <ul className="mt-1 list-disc pl-5">
          <li>Template namespace: Secrets that a template's resources copy to its clusters (for example a DNS token for external-dns).</li>
          <li>
            Environment: credentials a Cluster API provider reads (a Civo API key, Unraid SSH login, cloud keys). Providers only look in the cluster's own
            namespace, so these go in each environment you create clusters in. Secrets that a single cluster's extra resources copy go here too.
          </li>
        </ul>
      </div>
      <ErrorBanner error={error} />
      {errors.map((e, i) => (
        <Banner key={i} tone="error">
          {e}
        </Banner>
      ))}
      <div className="overflow-x-auto">
        <table className="table">
          <thead>
            <tr>
              <th>Name</th>
              <th>Namespace</th>
              <th>Keys</th>
              <th>Exists</th>
              <th>Needed by</th>
              <th>Delivered to</th>
              <th />
            </tr>
          </thead>
          <tbody>
            {!shown.length && (
              <tr>
                <td colSpan={7} className="text-muted">
                  No Secrets here, and no resource copies one.
                </td>
              </tr>
            )}
            {shown.map((r) => (
              <tr key={r.key}>
                <td>
                  {r.secret}
                  {r.type && r.type !== 'Opaque' && <div className="text-muted text-xs">{r.type}</div>}
                </td>
                <td>{r.ns}</td>
                <td className="text-xs">{r.keys.join(', ') || '-'}</td>
                <td className={r.missing ? 'text-error' : 'text-success'}>{r.missing ? 'Missing' : 'Present'}</td>
                <td>
                  {!r.neededBy.length && <span className="text-muted text-xs">not used by any resource</span>}
                  {r.neededBy.map((n) => (
                    <div key={n}>{n}</div>
                  ))}
                </td>
                <td>
                  {!r.clusters.length && <span>-</span>}
                  {r.clusters.map((c) => (
                    <div key={idOf(c)}>
                      {c.metadata.namespace}/{c.metadata.name}
                    </div>
                  ))}
                </td>
                <td className="text-right whitespace-nowrap">
                  <div className="flex items-center justify-end gap-1">
                    {confirming === r.key ? (
                      <>
                        <span className="mr-1.5 text-xs">Delete {r.secret}?</span>
                        <button type="button" className="btn btn-danger btn-sm" onClick={() => onRemove(r)}>
                          Delete
                        </button>
                        <button type="button" className="btn btn-tertiary btn-sm" onClick={() => setConfirming('')}>
                          Cancel
                        </button>
                      </>
                    ) : r.missing ? (
                      <button type="button" className="btn btn-secondary btn-sm" onClick={() => setEditing({ existing: false, namespace: r.ns, name: r.secret })}>
                        Create
                      </button>
                    ) : (
                      <>
                        <button type="button" className="btn btn-tertiary btn-sm" onClick={() => setViewing(r)}>
                          View
                        </button>
                        <button type="button" className="btn btn-tertiary btn-sm" onClick={() => setEditing({ existing: true, namespace: r.ns, name: r.secret })}>
                          Edit
                        </button>
                        <button type="button" className="btn btn-tertiary btn-sm" onClick={() => setConfirming(r.key)}>
                          Delete
                        </button>
                      </>
                    )}
                  </div>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      {viewing && <SecretView namespace={viewing.ns} name={viewing.secret} onClose={() => setViewing(null)} />}
      {editing && (
        <SecretEdit
          existing={editing.existing}
          namespace={editing.namespace}
          name={editing.name}
          namespaces={data?.namespaces || []}
          templateNamespaces={tplNS}
          onClose={() => setEditing(null)}
          onSaved={async () => {
            setEditing(null)
            await reload(true)
          }}
        />
      )}
    </div>
  )
}
