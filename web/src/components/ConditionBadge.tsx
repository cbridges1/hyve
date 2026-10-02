import type { Condition } from '../lib/api/types'

const colors: Record<string, string> = {
  True: 'bg-green-100 text-green-800 dark:bg-green-950 dark:text-green-300',
  False: 'bg-red-100 text-red-800 dark:bg-red-950 dark:text-red-300',
  Unknown: 'bg-neutral-100 text-neutral-600 dark:bg-neutral-800 dark:text-neutral-400',
}

/** Renders the "Ready" condition as a compact badge — hyve sets Ready/Reconciling/Error per ClusterDefinitionStatus.Conditions' own doc comment. Falls back to "Unknown" when no conditions have been reported yet (e.g. the controller hasn't reconciled this generation). */
export function ReadyBadge({ conditions }: { conditions?: Condition[] }) {
  const ready = conditions?.find((c) => c.type === 'Ready')
  const status = ready?.status ?? 'Unknown'
  return (
    <span
      title={ready?.message}
      className={`inline-block rounded px-2 py-0.5 text-xs font-medium ${colors[status]}`}
    >
      {status === 'True' ? 'Ready' : status === 'False' ? 'Not ready' : 'Unknown'}
    </span>
  )
}

export function RefStatusBadge({ resolved, error }: { resolved: boolean; error?: string }) {
  if (error) {
    return (
      <span
        title={error}
        className="inline-block rounded bg-red-100 px-2 py-0.5 text-xs font-medium text-red-800 dark:bg-red-950 dark:text-red-300"
      >
        error
      </span>
    )
  }
  return (
    <span
      className={`inline-block rounded px-2 py-0.5 text-xs font-medium ${
        resolved
          ? 'bg-green-100 text-green-800 dark:bg-green-950 dark:text-green-300'
          : 'bg-neutral-100 text-neutral-600 dark:bg-neutral-800 dark:text-neutral-400'
      }`}
    >
      {resolved ? 'resolved' : 'unresolved'}
    </span>
  )
}
