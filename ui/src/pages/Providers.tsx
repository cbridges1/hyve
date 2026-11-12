// The installed Cluster API providers and their health (lib/providers.ts).
// Adding one does what 'hyve provider add' does; each opens its own page
// (ProviderPage) with its details, its ClusterClasses and the credentials
// their clusters read.
import { useState } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import { ProviderAdd } from '../components/ProviderAdd'
import { Banner, ErrorBanner, Loading, PageHeader } from '../components/ui'
import { useLoad } from '../lib/hooks'
import { loadProviders } from '../lib/providers'
import { paths } from '../lib/routes'

export default function Providers() {
  const { data, error, loading, reload } = useLoad(loadProviders, [])
  const [adding, setAdding] = useState(false)
  const [message, setMessage] = useState('')
  const navigate = useNavigate()

  if (loading) return <Loading />
  const rows = data || []
  return (
    <div>
      <PageHeader
        title="Providers"
        subtitle="The Cluster API providers on this cluster and their controllers' health. Open one for its ClusterClasses and the credentials their clusters read."
      >
        {!adding && (
          <button
            type="button"
            className="btn btn-primary"
            onClick={() => {
              setAdding(true)
              setMessage('')
            }}
          >
            Add provider
          </button>
        )}
      </PageHeader>
      {message && <Banner tone="success">{message}</Banner>}
      <ErrorBanner error={error} />
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
            </tr>
          </thead>
          <tbody>
            {!rows.length && (
              <tr>
                <td colSpan={5} className="text-muted">
                  No Cluster API providers installed.
                </td>
              </tr>
            )}
            {rows.map((p) => (
              <tr key={p.id} className="cursor-pointer" onClick={() => navigate(paths.provider(p.ns, p.full))}>
                <td>
                  <Link to={paths.provider(p.ns, p.full)} onClick={(e) => e.stopPropagation()}>
                    {p.name}
                  </Link>
                </td>
                <td>{p.type}</td>
                <td>{p.ns}</td>
                <td>{p.version}</td>
                <td className={p.healthy ? 'text-success' : 'text-warning'}>{p.healthy ? 'Ready' : `Not ready (${p.state})`}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  )
}
