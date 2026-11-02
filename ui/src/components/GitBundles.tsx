// The bundles from git repos that target one Fleet cluster, with their
// state there; opened, what each deployed there (components/BundleDetail).
// Read-only, since git owns them; each links to its repo.
// GitSection puts them under the repos themselves, on a cluster's
// Resources tab.
import { Fragment, useState } from 'react'
import { Link } from 'react-router-dom'
import { BundleDetail } from './BundleDetail'
import { Icon } from './Icon'
import { RepoManager, type RepoScope } from './RepoManager'
import { idOf } from '../lib/kube'
import { bundleMessage, type RepoBundle } from '../lib/repos'
import { stateColor } from '../lib/resources'
import { paths } from '../lib/routes'

export function GitBundles({ fleetId, bundles, backLabel }: { fleetId: string; bundles: RepoBundle[]; backLabel: string }) {
  const [open, setOpen] = useState<Set<string>>(new Set())
  const toggle = (id: string) =>
    setOpen((o) => {
      const n = new Set(o)
      if (n.has(id)) n.delete(id)
      else n.add(id)
      return n
    })
  return (
    <table className="table">
      <thead>
        <tr>
          <th />
          <th>Bundle</th>
          <th>Repo</th>
          <th>State here</th>
          <th>Objects</th>
        </tr>
      </thead>
      <tbody>
        {bundles.map((g) => {
          const id = idOf(g.bundle)
          const state = g.on[fleetId]
          const bd = g.bds[fleetId]
          const message = state !== 'Ready' ? bundleMessage(g.bundle) : ''
          return (
            <Fragment key={id}>
              <tr className="cursor-pointer" onClick={() => toggle(id)}>
                <td className="w-6">
                  <span className={`inline-block transition-transform ${open.has(id) ? 'rotate-90' : ''}`}>
                    <Icon name="chevronRight" size={14} />
                  </span>
                </td>
                <td>{g.name}</td>
                <td>
                  <Link to={paths.repo(g.repo.metadata.namespace!, g.repo.metadata.name)} onClick={(e) => e.stopPropagation()}>
                    {g.repo.metadata.name}
                  </Link>
                </td>
                <td>
                  <span className={stateColor(state)}>{state}</span>
                  {message && !open.has(id) && (
                    <div className="text-muted line-clamp-2 max-w-xl text-xs" title={message}>
                      {message}
                    </div>
                  )}
                </td>
                <td>{bd?.status?.resources?.length ?? '-'}</td>
              </tr>
              {open.has(id) && (
                <tr>
                  <td />
                  <td colSpan={4}>
                    <BundleDetail bundle={g.bundle} bds={bd ? [bd] : []} backLabel={backLabel} />
                  </td>
                </tr>
              )}
            </Fragment>
          )
        })}
      </tbody>
    </table>
  )
}

export function GitSection({ fleetId, scope, bundles, backLabel }: { fleetId: string; scope: RepoScope; bundles: RepoBundle[]; backLabel: string }) {
  return (
    <div className="mt-8">
      <RepoManager
        scope={scope}
        header={(add) => (
          <div className="mb-2 flex items-center justify-between gap-3">
            <div>
              <h3 className="mt-0 mb-0.5">Git repos</h3>
              <p className="text-muted mb-0 text-sm">The repos that deliver here. Editing one changes it on every cluster it deploys to.</p>
            </div>
            {add}
          </div>
        )}
      />
      {bundles.length > 0 && (
        <>
          <h3 className="mt-6">Bundles from them</h3>
          <p className="text-muted mb-2 text-sm">What git owns here, read-only: a change goes in the repo. Open one for the objects it deployed here.</p>
          <GitBundles fleetId={fleetId} bundles={bundles} backLabel={backLabel} />
        </>
      )}
    </div>
  )
}
