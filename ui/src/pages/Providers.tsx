// The installed Cluster API providers and their health, however they were
// installed (the CAPI Operator, or local mode's lean install): every
// provider's controller is a Deployment labelled with its name. Adding and
// removing do what 'hyve provider add/remove' do. Its Credentials tab,
// /providers/credentials, is the Secrets providers read each cluster's
// credentials from, in its environment, to make before the first cluster
// there.
import { useState } from 'react'
import { useParams } from 'react-router-dom'
import { ProviderAdd } from '../components/ProviderAdd'
import { SecretsSection } from '../components/SecretsSection'
import { Banner, ErrorBanner, Loading, PageHeader, Tabs } from '../components/ui'
import { ViewDialog, type Section } from '../components/ViewDialog'
import { useAction, useLoad } from '../lib/hooks'
import { useEnvironment, useEnvironments } from '../lib/environment'
import { idOf, kube, remove, schemaFor, typeOf, type Obj, type Reader } from '../lib/kube'
import { paths } from '../lib/routes'
import { providerCredentials } from '../lib/secrets'

const DEPLOYMENT = 'apps.deployment'
const PROVIDER_LABEL = 'cluster.x-k8s.io/provider'
// The CAPI Operator's provider kinds.
const OPERATOR_TYPES = ['core', 'bootstrap', 'controlplane', 'infrastructure', 'addon', 'ipam', 'runtimeextension'].map(
  (k) => `operator.cluster.x-k8s.io.${k}provider`,
)
const KINDS = ['infrastructure', 'bootstrap', 'control-plane', 'addon', 'ipam', 'runtime-extension']

type Row = { d: Obj; id: string; name: string; type: string; ns: string; version: string; healthy: boolean; state: string }

function toRow(d: Obj): Row {
  const full = d.metadata.labels![PROVIDER_LABEL]
  let type = full === 'fleet' ? 'addon' : 'core'
  let name = full
  KINDS.forEach((t) => {
    if (full.startsWith(`${t}-`)) {
      type = t
      name = full.slice(t.length + 1)
    }
  })
  const image: string = d.spec?.template?.spec?.containers?.[0]?.image || ''
  const i = image.lastIndexOf(':')
  const want = d.spec?.replicas ?? 1
  const ready = d.status?.availableReplicas || 0
  return {
    d,
    id: idOf(d),
    name,
    type,
    ns: d.metadata.namespace || '',
    version: i > image.lastIndexOf('/') ? image.slice(i + 1) : '',
    healthy: ready >= want && want > 0,
    state: `${ready}/${want}`,
  }
}

// The CAPI Operator's provider objects: what removing a provider deletes.
async function providerObjects(r: Reader): Promise<Obj[]> {
  const out: Obj[] = []
  for (const type of OPERATOR_TYPES) {
    if (schemaFor(type)) out.push(...(await r.findAll(type, { force: true }).catch(() => [] as Obj[])))
  }
  return out
}

export default function Providers() {
  const { tab } = useParams()
  const credentials = tab === 'credentials'
  const [adding, setAdding] = useState(false)
  return (
    <div>
      <PageHeader
        title="Providers"
        subtitle={
          credentials
            ? "The Secrets providers read each cluster's credentials from: a ClusterClass's credentialsRef, such as Civo's credentialsSecret. A provider reads it in the cluster's environment, so make it there before the first cluster."
            : "The Cluster API providers on this cluster and their controllers' health. Adding one does what 'hyve provider add' does: through the Cluster API Operator or, in local mode, hyve's own manifests."
        }
      >
        {!credentials && !adding && (
          <button type="button" className="btn btn-primary" onClick={() => setAdding(true)}>
            Add provider
          </button>
        )}
      </PageHeader>
      <Tabs
        tabs={[
          { label: 'Installed', to: paths.providers, active: !credentials },
          { label: 'Credentials', to: paths.providerCredentials, active: credentials },
        ]}
      />
      {credentials ? <Credentials /> : <Installed adding={adding} setAdding={setAdding} />}
    </div>
  )
}

// What each ClusterClass's clusters read their credentials from, in the
// environment the header picks, or every one.
function Credentials() {
  const [env] = useEnvironment()
  const envs = useEnvironments()
  if (envs.loading) return <Loading />
  const shown = env ? [env] : envs.data || []
  return (
    <>
      <ErrorBanner error={envs.error} />
      <SecretsSection
        load={(r) => providerCredentials(r, shown)}
        deps={[shown.join(',')]}
        hint="In the environment: the provider reads it for every cluster made there."
        empty="No ClusterClass here reads its credentials from a Secret: its provider takes them another way, such as its own variables."
        showNamespace={shown.length > 1}
      />
    </>
  )
}

function Installed({ adding, setAdding }: { adding: boolean; setAdding: (b: boolean) => void }) {
  const { data, error, loading, reload } = useLoad(async (r) => {
    const deployments = await r.findAll(DEPLOYMENT, { force: true }).catch(() => [] as Obj[])
    const rows = deployments
      .filter((d) => d.metadata.labels?.[PROVIDER_LABEL])
      .map(toRow)
      .sort((a, b) => `${a.type}${a.name}`.localeCompare(`${b.type}${b.name}`))
    return { rows, objects: await providerObjects(r) }
  }, [])
  const [message, setMessage] = useState('')
  const [confirming, setConfirming] = useState('')
  const [viewing, setViewing] = useState<{ title: string; sections: Section[] } | null>(null)
  const act = useAction()

  const objects = data?.objects || []
  const objectFor = (r: Row) =>
    objects.find((p) => p.metadata.namespace === r.ns && p.metadata.name === r.name) ||
    objects.find((p) => p.metadata.namespace === r.ns && (p.spec?.name || p.metadata.name) === r.name)

  const removeProvider = (r: Row) => {
    setConfirming('')
    act.run(async () => {
      const o = objectFor(r)!
      await remove(typeOf(o)!, o)
      setMessage('')
      await reload(true)
    })
  }

  // A provider's controller, and the CAPI Operator object it was installed
  // from when there is one.
  const view = (r: Row) =>
    act.run(async () => {
      const sections: Section[] = []
      for (const type of OPERATOR_TYPES) {
        if (!schemaFor(type)) continue
        const all = await kube.findAll(type).catch(() => [] as Obj[])
        all.filter((p) => p.metadata.namespace === r.ns).forEach((p) => sections.push({ label: p.kind || type, object: p }))
      }
      sections.push({ label: 'Deployment', object: r.d })
      setViewing({ title: `${r.type} ${r.name}`, sections })
    })

  if (loading) return <Loading />
  const rows = data?.rows || []
  return (
    <div>
      {message && <Banner tone="success">{message}</Banner>}
      <ErrorBanner error={error} />
      <ErrorBanner error={act.error} />
      {adding && (
        <ProviderAdd
          onClose={() => setAdding(false)}
          onAdded={(msg) => {
            setAdding(false)
            setMessage(`${msg}. It shows here once its controller is running.`)
            reload(true)
          }}
        />
      )}
      <div className="overflow-x-auto">
        <table className="table">
          <thead>
            <tr>
              <th>Name</th>
              <th>Type</th>
              <th>Namespace</th>
              <th>Version</th>
              <th>Health</th>
              <th />
            </tr>
          </thead>
          <tbody>
            {!rows.length && (
              <tr>
                <td colSpan={6} className="text-muted">
                  No Cluster API providers installed.
                </td>
              </tr>
            )}
            {rows.map((r) => (
              <tr key={r.id}>
                <td>{r.name}</td>
                <td>{r.type}</td>
                <td>{r.ns}</td>
                <td>{r.version}</td>
                <td className={r.healthy ? 'text-success' : 'text-warning'}>{r.healthy ? 'Ready' : `Not ready (${r.state})`}</td>
                <td className="text-right whitespace-nowrap">
                  {confirming === r.id ? (
                    <>
                      <span className="mr-1.5 text-xs">Remove {r.name}? Delete its clusters first.</span>
                      <button type="button" className="btn btn-danger btn-sm" disabled={act.busy} onClick={() => removeProvider(r)}>
                        Remove
                      </button>
                      <button type="button" className="btn btn-tertiary btn-sm" onClick={() => setConfirming('')}>
                        Cancel
                      </button>
                    </>
                  ) : (
                    <>
                      <button type="button" className="btn btn-tertiary btn-sm" onClick={() => view(r)}>
                        View
                      </button>
                      {objectFor(r) && (
                        <button type="button" className="btn btn-tertiary btn-sm" onClick={() => setConfirming(r.id)}>
                          Remove
                        </button>
                      )}
                    </>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      {viewing && <ViewDialog title={viewing.title} sections={viewing.sections} onClose={() => setViewing(null)} />}
    </div>
  )
}
