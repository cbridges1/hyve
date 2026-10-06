import type { AgentState, Condition } from '../lib/api/types'

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
    <span title={ready?.message} className={`inline-block rounded px-2 py-0.5 text-xs font-medium ${colors[status]}`}>
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

const tone = {
  good: 'bg-green-100 text-green-800 dark:bg-green-950 dark:text-green-300',
  busy: 'bg-amber-100 text-amber-800 dark:bg-amber-950 dark:text-amber-300',
  bad: 'bg-red-100 text-red-800 dark:bg-red-950 dark:text-red-300',
  idle: 'bg-neutral-100 text-neutral-600 dark:bg-neutral-800 dark:text-neutral-400',
}

const phases: Record<string, { label: string; tone: keyof typeof tone }> = {
  ACTIVE: { label: 'Active', tone: 'good' },
  CREATING: { label: 'Creating', tone: 'busy' },
  UPDATING: { label: 'Updating', tone: 'busy' },
  DELETING: { label: 'Deleting', tone: 'busy' },
  NOT_FOUND: { label: 'Not created', tone: 'idle' },
  FAILED: { label: 'Failed', tone: 'bad' },
}

/** The cluster's own state (status.phase) — Creating, Active, ... — or "Error" when the last reconcile failed, whatever the phase. Hover for the detail. Falls back to the Ready condition for a cluster no phase has been recorded for yet. */
export function ClusterStatusBadge({ cluster }: { cluster: { phase?: string; conditions?: Condition[] } }) {
  const ready = cluster.conditions?.find((c) => c.type === 'Ready')
  if (ready?.reason === 'ReconcileFailed') {
    return <Pill tone="bad" title={ready.message} label="Error" />
  }
  const p = cluster.phase ? phases[cluster.phase] : undefined
  if (!p) return <ReadyBadge conditions={cluster.conditions} />
  return <Pill tone={p.tone} title={ready?.message} label={p.label} />
}

const agentStates: Record<AgentState, { label: string; tone: keyof typeof tone; title: string }> = {
  waiting: {
    label: 'Agent: waiting for cluster',
    tone: 'idle',
    title: 'hyve-agent is installed once the cluster is active',
  },
  installing: {
    label: 'Agent: installing',
    tone: 'busy',
    title: "hyve-agent is being installed, or hasn't connected yet",
  },
  connected: {
    label: 'Agent connected',
    tone: 'good',
    title: 'hyve-agent is connected',
  },
  disconnected: {
    label: 'Agent disconnected',
    tone: 'bad',
    title: 'hyve-agent was connected before and isn’t now',
  },
}

/** hyve-agent's state, for a cluster with the agent enabled — kept apart from the cluster's own status so "not ready" and "agent not connected yet" never look alike. */
export function AgentBadge({ state }: { state?: AgentState }) {
  if (!state) return null
  const a = agentStates[state]
  return <Pill tone={a.tone} title={a.title} label={a.label} />
}

function Pill({ tone: t, title, label }: { tone: keyof typeof tone; title?: string; label: string }) {
  return (
    <span title={title} className={`inline-block rounded px-2 py-0.5 text-xs font-medium ${tone[t]}`}>
      {label}
    </span>
  )
}

/** When the cluster is scheduled to be deleted — "Expires in 3h 20m", the exact local time on hover. Nothing for a cluster that never expires. */
export function ExpiryBadge({ expiresAt }: { expiresAt?: string }) {
  if (!expiresAt) return null
  const at = new Date(expiresAt)
  if (Number.isNaN(at.getTime())) return null
  const ms = at.getTime() - Date.now()
  const title = `Scheduled for deletion at ${at.toLocaleString()}`
  if (ms <= 0) return <Pill tone="busy" title={title} label="Expired — deleting" />
  return <Pill tone="idle" title={title} label={`Expires in ${formatDuration(ms)}`} />
}

function formatDuration(ms: number): string {
  const minutes = Math.ceil(ms / 60_000)
  if (minutes < 60) return `${minutes}m`
  const hours = Math.floor(minutes / 60)
  if (hours < 24) return `${hours}h ${minutes % 60}m`
  return `${Math.floor(hours / 24)}d ${hours % 24}h`
}
