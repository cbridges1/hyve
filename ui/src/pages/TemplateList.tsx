// ClusterTemplates: open one, make a cluster from it, or delete it.
import { useState } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import { ApplyYaml } from '../components/ApplyYaml'
import { ConfirmButton, ErrorBanner, Loading, PageHeader, Pill } from '../components/ui'
import { useAction, useLoad } from '../lib/hooks'
import { idOf, remove } from '../lib/kube'
import { paths } from '../lib/routes'
import { readyDisplay, templateSummary } from '../lib/templates'
import { TYPES } from '../lib/types'

export default function TemplateList() {
  const navigate = useNavigate()
  const [applying, setApplying] = useState(false)
  const { error, run } = useAction()
  const { data: templates, error: loadError, loading, reload } = useLoad(
    async (r) => (await r.findAll(TYPES.TEMPLATE, { force: true })).sort((a, b) => idOf(a).localeCompare(idOf(b))),
    [],
  )

  return (
    <div>
      <PageHeader title="Templates">
        <Link className="btn btn-secondary" to={paths.import}>
          Import
        </Link>
        <button type="button" className="btn btn-secondary" onClick={() => setApplying(true)}>
          Apply YAML
        </button>
        <Link className="btn btn-primary" to={paths.newTemplate()}>
          New template
        </Link>
      </PageHeader>
      {applying && <ApplyYaml onClose={() => setApplying(false)} onApplied={() => reload(true)} />}
      <ErrorBanner error={error || loadError} />
      {loading ? (
        <Loading />
      ) : (
        <div className="overflow-hidden rounded-xl border border-neutral-200 bg-white shadow-sm dark:border-neutral-800 dark:bg-neutral-900">
          {!templates?.length && <p className="p-6 text-center text-sm text-neutral-500">No templates yet.</p>}
          <div className="divide-y divide-neutral-100 dark:divide-neutral-800">
            {templates?.map((tpl) => {
              const ready = readyDisplay(tpl)
              const { namespace, name } = tpl.metadata
              return (
                <div
                  key={idOf(tpl)}
                  className="flex cursor-pointer flex-col gap-2 px-4 py-3 transition-colors hover:bg-neutral-50 sm:flex-row sm:items-center sm:justify-between dark:hover:bg-neutral-800/50"
                  onClick={() => navigate(paths.template(namespace!, name))}
                >
                  <div className="min-w-0">
                    <div className="font-medium text-neutral-900 dark:text-neutral-100">{name}</div>
                    <div className="truncate text-xs text-neutral-500">{tpl.spec?.description || templateSummary(tpl)}</div>
                  </div>
                  <div className="flex shrink-0 items-center gap-3">
                    <span className="text-xs text-neutral-500">{namespace}</span>
                    <Pill tone={ready === 'Ready' ? 'good' : 'bad'} label={ready === 'Ready' ? 'Ready' : 'Not ready'} title={ready} />
                    <button
                      type="button"
                      className="btn btn-tertiary btn-sm"
                      onClick={(e) => {
                        e.stopPropagation()
                        navigate(paths.createCluster({ namespace: namespace!, name }))
                      }}
                    >
                      New cluster
                    </button>
                    <ConfirmButton
                      label="Delete"
                      confirm={`Delete ${name}?`}
                      onConfirm={() =>
                        run(async () => {
                          await remove(TYPES.TEMPLATE, tpl)
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
