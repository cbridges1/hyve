import type { WorkflowRef, WorkflowsSpec } from '../lib/api/types'

function refLabel(ref: WorkflowRef): string {
  if (ref.name) return ref.name
  if (ref.source) return ref.path ? `${ref.source} (${ref.path})` : ref.source
  return '(unnamed)'
}

const hookLabels: { key: keyof WorkflowsSpec; label: string }[] = [
  { key: 'beforeCreate', label: 'Before create' },
  { key: 'onCreate', label: 'On create' },
  { key: 'afterCreate', label: 'After create' },
  { key: 'onDelete', label: 'On delete' },
  { key: 'afterDelete', label: 'After delete' },
  { key: 'preReconcile', label: 'Pre-reconcile' },
]

/** Renders every non-empty lifecycle hook in a WorkflowsSpec — shared between templates and (eventually) cluster definitions, the two places these hooks show up. */
export function WorkflowHooks({ workflows }: { workflows?: WorkflowsSpec }) {
  const entries = hookLabels
    .map(({ key, label }) => ({ label, refs: workflows?.[key] }))
    .filter((e) => e.refs && e.refs.length > 0)

  if (entries.length === 0) {
    return <p className="text-sm text-neutral-500 dark:text-neutral-500">No lifecycle hooks configured.</p>
  }

  return (
    <div className="space-y-2">
      {entries.map(({ label, refs }) => (
        <div key={label} className="flex flex-col gap-1 sm:flex-row sm:gap-3">
          <span className="w-32 shrink-0 text-xs font-medium text-neutral-500 dark:text-neutral-500">{label}</span>
          <div className="flex flex-wrap gap-1.5">
            {refs!.map((ref, i) => (
              <span
                key={i}
                className="rounded-md bg-neutral-100 px-2 py-0.5 font-mono text-xs text-neutral-700 dark:bg-neutral-800 dark:text-neutral-300"
              >
                {refLabel(ref)}
              </span>
            ))}
          </div>
        </div>
      ))}
    </div>
  )
}
