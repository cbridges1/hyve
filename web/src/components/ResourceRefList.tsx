import type { ResourceRef } from '../lib/api/types'
import { EmptyState } from './Card'

function describeResourceRef(r: ResourceRef): string {
  if (r.helm) return `helm: ${r.helm.chart}${r.helm.version ? `@${r.helm.version}` : ''}${r.helm.repo ? ` (${r.helm.repo})` : ''}`
  if (r.secret) return `secret: ${r.secret.keys.length} key(s)`
  if (r.source) return r.source
  return '(resolved by name)'
}

/** Renders a ClusterDefinition/Template's spec.resources[] — the embedded-reference form, distinct from the standalone Resource CRD type shown on ResourcesPage. */
export function ResourceRefList({ resources }: { resources?: ResourceRef[] }) {
  if (!resources?.length) return <EmptyState>No resources declared.</EmptyState>

  return (
    <div className="divide-y divide-neutral-100 dark:divide-neutral-800/70">
      {resources.map((r) => (
        <div key={r.name} className="flex flex-col gap-0.5 py-2.5 first:pt-0 last:pb-0 sm:flex-row sm:items-baseline sm:justify-between">
          <span className="font-medium text-neutral-900 dark:text-neutral-100">{r.name}</span>
          <span className="font-mono text-xs text-neutral-500 dark:text-neutral-500">
            {describeResourceRef(r)}
            {r.namespace ? ` · ns/${r.namespace}` : ''}
            {r.delete ? ' · marked for delete' : ''}
          </span>
        </div>
      ))}
    </div>
  )
}
