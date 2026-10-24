// Any kind the cluster serves, listed; each object opens as YAML.
import { Link, useParams } from 'react-router-dom'
import { ErrorBanner, Loading, PageHeader } from '../components/ui'
import { age } from '../lib/format'
import { useLoad } from '../lib/hooks'
import { can, idOf, schemaFor } from '../lib/kube'
import { detailPath, paths } from '../lib/routes'

export default function GenericList() {
  const type = useParams().type!
  const schema = schemaFor(type)
  const { data: rows, error, loading } = useLoad(
    async (r) => (await r.findAll(type, { force: true })).sort((a, b) => idOf(a).localeCompare(idOf(b))),
    [type],
  )
  if (loading) return <Loading />
  return (
    <div>
      <PageHeader title={schema?.kind || type}>
        {can(type, 'create') && (
          <Link className="btn btn-primary" to={paths.newYaml(type)}>
            New
          </Link>
        )}
      </PageHeader>
      <ErrorBanner error={error} />
      <table className="table">
        <thead>
          <tr>
            <th>Name</th>
            {schema?.namespaced && <th>Namespace</th>}
            <th>Age</th>
          </tr>
        </thead>
        <tbody>
          {!rows?.length && (
            <tr>
              <td colSpan={3} className="text-muted">
                Nothing here yet.
              </td>
            </tr>
          )}
          {rows?.map((r) => (
            <tr key={idOf(r)}>
              <td>
                <Link to={detailPath(type, r)}>{r.metadata.name}</Link>
              </td>
              {schema?.namespaced && <td>{r.metadata.namespace}</td>}
              <td>{age(r.metadata.creationTimestamp)}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}
