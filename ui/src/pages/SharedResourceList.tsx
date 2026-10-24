// Shared Resources: Fleet specs templates and clusters refer to by name.
import { useState } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import { ApplyYaml } from '../components/ApplyYaml'
import { ConfirmButton, ErrorBanner, Loading, PageHeader, Pill } from '../components/ui'
import { useAction, useLoad } from '../lib/hooks'
import { idOf, remove } from '../lib/kube'
import { paths } from '../lib/routes'
import { fleetKind, secretsDisplay, sourceDisplay } from '../lib/templates'
import { TYPES } from '../lib/types'

export default function SharedResourceList() {
  const navigate = useNavigate()
  const [applying, setApplying] = useState(false)
  const { error, run } = useAction()
  const { data: rows, error: loadError, loading, reload } = useLoad(
    async (r) => (await r.findAll(TYPES.RESOURCE, { force: true })).sort((a, b) => idOf(a).localeCompare(idOf(b))),
    [],
  )

  return (
    <div>
      <PageHeader title="Resources">
        <button type="button" className="btn btn-secondary" onClick={() => setApplying(true)}>
          Apply YAML
        </button>
        <Link className="btn btn-primary" to={paths.newResource()}>
          New resource
        </Link>
      </PageHeader>
      {applying && <ApplyYaml onClose={() => setApplying(false)} onApplied={() => reload(true)} />}
      <p className="mb-4 text-sm text-neutral-500">
        Fleet specs that templates and clusters refer to by name: a Helm chart, git paths or manifests. Also <code>hyve resource create</code>.
      </p>
      <ErrorBanner error={error || loadError} />
      {loading ? (
        <Loading />
      ) : (
        <div className="overflow-hidden rounded-xl border border-neutral-200 bg-white shadow-sm dark:border-neutral-800 dark:bg-neutral-900">
          {!rows?.length && <p className="p-6 text-center text-sm text-neutral-500">No resources yet.</p>}
          <div className="divide-y divide-neutral-100 dark:divide-neutral-800">
            {rows?.map((r) => {
              const secrets = secretsDisplay(r)
              return (
                <div
                  key={idOf(r)}
                  className="flex cursor-pointer items-center justify-between gap-3 px-4 py-3 transition-colors hover:bg-neutral-50 dark:hover:bg-neutral-800/50"
                  onClick={() => navigate(paths.resource(r.metadata.namespace!, r.metadata.name))}
                >
                  <div className="min-w-0">
                    <div className="font-medium text-neutral-900 dark:text-neutral-100">{r.metadata.name}</div>
                    <div className="truncate text-xs text-neutral-500">
                      {sourceDisplay(r)}
                      {secrets && ` · secrets ${secrets}`}
                    </div>
                  </div>
                  <div className="flex shrink-0 items-center gap-3">
                    <span className="text-xs text-neutral-500">{r.metadata.namespace}</span>
                    <Pill tone="info" label={fleetKind(r) || '?'} />
                    <ConfirmButton
                      label="Delete"
                      confirm={`Delete ${r.metadata.name}?`}
                      onConfirm={() =>
                        run(async () => {
                          await remove(TYPES.RESOURCE, r)
                          await reload(true)
                        })
                      }
                    />
                  </div>
                </div>
              )
            })}
          </div>
        </div>
      )}
    </div>
  )
}
