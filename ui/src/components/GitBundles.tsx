// The bundles from git repos that target one Fleet cluster, with their
// state there: read-only, since git owns them; each links to its repo.
import { Link } from 'react-router-dom'
import { idOf } from '../lib/kube'
import { bundleMessage, type RepoBundle } from '../lib/repos'
import { stateColor } from '../lib/resources'
import { paths } from '../lib/routes'

export function GitBundles({ fleetId, bundles }: { fleetId: string; bundles: RepoBundle[] }) {
  return (
    <table className="table">
      <thead>
        <tr>
          <th>Bundle</th>
          <th>Repo</th>
          <th>State here</th>
        </tr>
      </thead>
      <tbody>
        {bundles.map((g) => {
          const state = g.on[fleetId]
          const message = state !== 'Ready' ? bundleMessage(g.bundle) : ''
          return (
            <tr key={idOf(g.bundle)}>
              <td>{g.name}</td>
              <td>
                <Link to={paths.repo(g.repo.metadata.namespace!, g.repo.metadata.name)}>{g.repo.metadata.name}</Link>
              </td>
              <td>
                <span className={stateColor(state)}>{state}</span>
                {message && (
                  <div className="text-muted line-clamp-2 max-w-xl text-xs" title={message}>
                    {message}
                  </div>
                )}
              </td>
            </tr>
          )
        })}
      </tbody>
    </table>
  )
}
