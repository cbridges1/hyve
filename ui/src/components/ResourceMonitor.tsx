// A cluster's resource monitor, like Rancher's cluster dashboard: CPU,
// memory and pods as gauges (used, requested and limits against what the
// nodes can give), each node's health and load, and recent warning
// events. It reads again every 15 seconds while the page is in view.
// Without metrics-server on the cluster, "used" is unknown and says so.
import { age } from '../lib/format'
import { usePolled } from '../lib/hooks'
import { bytes, cores, loadResources, percent, type ClusterResources, type Usage } from '../lib/metrics'
import { Card, EmptyState, ErrorBanner, Loading, Pill } from './ui'
import { Table } from './Table'

const EVERY = 15_000

// The bar's colour by how full it is.
const fill = (p: number) => (p >= 90 ? 'bg-red-500' : p >= 75 ? 'bg-amber-500' : 'bg-emerald-500')

// A bar: the filled part, a lighter one behind it (requested behind used),
// and a tick for limits.
function Bar({ value, behind, tick, label }: { value: number; behind?: number; tick?: number; label: string }) {
  const clamp = (n: number) => Math.max(0, Math.min(100, n))
  return (
    <div className="relative h-2 w-full overflow-hidden rounded-full bg-neutral-100 dark:bg-neutral-800" role="meter" aria-label={label} aria-valuenow={value} aria-valuemin={0} aria-valuemax={100}>
      {behind !== undefined && <div className="absolute inset-y-0 left-0 bg-sky-200 dark:bg-sky-900" style={{ width: `${clamp(behind)}%` }} />}
      <div className={`absolute inset-y-0 left-0 rounded-full ${fill(value)}`} style={{ width: `${clamp(value)}%` }} />
      {tick !== undefined && tick > 0 && <div className="absolute inset-y-0 w-0.5 bg-neutral-500" style={{ left: `${clamp(tick)}%` }} />}
    </div>
  )
}

function Gauge({ title, u, fmt, metrics }: { title: string; u: Usage; fmt: (n: number) => string; metrics: boolean }) {
  const used = u.used === null ? null : percent(u.used, u.allocatable)
  const requested = percent(u.requested, u.allocatable)
  const limits = percent(u.limits, u.allocatable)
  return (
    <div className="rounded-xl border border-neutral-200 p-4 dark:border-neutral-800">
      <div className="mb-3 flex items-baseline justify-between gap-2">
        <h3 className="mb-0 text-sm font-semibold">{title}</h3>
        <span className="text-xs text-neutral-500">{fmt(u.allocatable)} allocatable</span>
      </div>
      <Row label="Used" value={used === null ? null : `${used}%`} detail={u.used === null ? (metrics ? '' : 'needs metrics-server') : `${fmt(u.used)}`}>
        {used !== null && <Bar value={used} label={`${title} used`} />}
      </Row>
      <Row label="Requested" value={`${requested}%`} detail={fmt(u.requested)}>
        <Bar value={requested} tick={limits} label={`${title} requested`} />
      </Row>
      <div className="mt-1 text-xs text-neutral-500">
        Limits {fmt(u.limits)} ({limits}%){u.capacity > u.allocatable ? ` · ${fmt(u.capacity)} on the nodes, ${fmt(u.capacity - u.allocatable)} reserved` : ''}
      </div>
    </div>
  )
}

function Row({ label, value, detail, children }: { label: string; value: string | null; detail: string; children?: React.ReactNode }) {
  return (
    <div className="mb-2">
      <div className="mb-1 flex items-baseline justify-between gap-2 text-xs">
        <span className="font-medium text-neutral-600 dark:text-neutral-300">{label}</span>
        <span className="text-neutral-500">
          {value !== null && <span className="mr-1 font-semibold text-neutral-800 dark:text-neutral-100">{value}</span>}
          {detail}
        </span>
      </div>
      {children}
    </div>
  )
}

function PodsGauge({ pods }: { pods: ClusterResources['pods'] }) {
  const p = percent(pods.running, pods.allocatable)
  return (
    <div className="rounded-xl border border-neutral-200 p-4 dark:border-neutral-800">
      <div className="mb-3 flex items-baseline justify-between gap-2">
        <h3 className="mb-0 text-sm font-semibold">Pods</h3>
        <span className="text-xs text-neutral-500">{pods.allocatable} allocatable</span>
      </div>
      <Row label="Running or pending" value={`${p}%`} detail={`${pods.running}`}>
        <Bar value={p} label="Pods" />
      </Row>
    </div>
  )
}

export function ResourceMonitor({ clusterId }: { clusterId: string }) {
  const { data, error, loading } = usePolled(() => loadResources(clusterId), EVERY, [clusterId])

  return (
    <Card
      title="Resources"
      action={data && <span className="text-xs text-neutral-500">{data.metrics ? 'Live, every 15 seconds' : 'Every 15 seconds; no metrics-server, so no usage'}</span>}
    >
      <ErrorBanner error={error && `Couldn't read the cluster: ${error}`} />
      {loading && !data ? (
        <Loading label="Reading the cluster…" />
      ) : (
        data && (
          <>
            <div className="mb-4 grid gap-3 md:grid-cols-3">
              <Gauge title="CPU" u={data.cpu} fmt={(n) => `${cores(n)} cores`} metrics={data.metrics} />
              <Gauge title="Memory" u={data.memory} fmt={bytes} metrics={data.metrics} />
              <PodsGauge pods={data.pods} />
            </div>
            {!data.metrics && (
              <p className="hint mb-4">
                Usage comes from metrics-server, which this cluster doesn't have. Deploy it (the <code>metrics-server</code> chart, from{' '}
                <code>https://kubernetes-sigs.github.io/metrics-server</code>) to see it here.
              </p>
            )}

            <h3 className="mb-2 text-sm font-semibold">Nodes</h3>
            <div className="mb-4 overflow-x-auto">
              <Table>
                <thead>
                  <tr>
                    <th>Name</th>
                    <th>State</th>
                    <th className="min-w-36">CPU</th>
                    <th className="min-w-36">Memory</th>
                    <th>Pods</th>
                    <th>Version</th>
                  </tr>
                </thead>
                <tbody>
                  {data.nodes.map((n) => {
                    const cpu = percent(n.cpu.used ?? n.cpu.requested, n.cpu.allocatable)
                    const mem = percent(n.memory.used ?? n.memory.requested, n.memory.allocatable)
                    const what = n.cpu.used === null ? 'requested' : 'used'
                    return (
                      <tr key={n.name}>
                        <td>
                          <div className="font-medium">{n.name}</div>
                          {n.roles.length > 0 && <div className="text-xs text-neutral-500">{n.roles.join(', ')}</div>}
                        </td>
                        <td>
                          <div className="flex flex-wrap gap-1">
                            <Pill tone={n.ready ? 'good' : 'bad'} label={n.ready ? 'Ready' : 'Not ready'} />
                            {n.problems.map((p) => (
                              <Pill key={p} tone="busy" label={p} />
                            ))}
                          </div>
                        </td>
                        <td title={`${cores(n.cpu.used ?? n.cpu.requested)} of ${cores(n.cpu.allocatable)} cores ${what}`}>
                          <div className="mb-1 text-xs text-neutral-500">
                            {cpu}% {what}
                          </div>
                          <Bar value={cpu} label={`${n.name} CPU`} />
                        </td>
                        <td title={`${bytes(n.memory.used ?? n.memory.requested)} of ${bytes(n.memory.allocatable)} ${what}`}>
                          <div className="mb-1 text-xs text-neutral-500">
                            {mem}% {what}
                          </div>
                          <Bar value={mem} label={`${n.name} memory`} />
                        </td>
                        <td className="whitespace-nowrap">
                          {n.pods.running}/{n.pods.allocatable}
                        </td>
                        <td className="whitespace-nowrap">{n.version}</td>
                      </tr>
                    )
                  })}
                </tbody>
              </Table>
            </div>

            <h3 className="mb-2 text-sm font-semibold">Recent warnings</h3>
            {data.events === null ? (
              <p className="hint">You can't read this cluster's events.</p>
            ) : data.events.length === 0 ? (
              <EmptyState>No warning events.</EmptyState>
            ) : (
              <div className="overflow-x-auto">
                <Table>
                  <thead>
                    <tr>
                      <th>When</th>
                      <th>Reason</th>
                      <th>Object</th>
                      <th>Message</th>
                    </tr>
                  </thead>
                  <tbody>
                    {data.events.map((e, i) => (
                      <tr key={i}>
                        <td className="whitespace-nowrap">{e.last ? `${age(e.last)} ago` : '-'}</td>
                        <td className="whitespace-nowrap">
                          {e.reason}
                          {e.count > 1 && <span className="text-xs text-neutral-500"> ×{e.count}</span>}
                        </td>
                        <td className="text-xs">
                          {e.namespace && <span className="text-neutral-500">{e.namespace}/</span>}
                          {e.object}
                        </td>
                        <td className="max-w-md text-xs break-words">{e.message}</td>
                      </tr>
                    ))}
                  </tbody>
                </Table>
              </div>
            )}
          </>
        )
      )}
    </Card>
  )
}

// One cluster's CPU, memory and pods in a row, for the overview: used
// where metrics-server is, else requested.
export function ResourceSummaryCells({ clusterId }: { clusterId: string }) {
  const { data, error } = usePolled(() => loadResources(clusterId, { events: false }), 30_000, [clusterId])
  if (error && !data)
    return (
      <td colSpan={3} className="text-xs text-neutral-500" title={error}>
        Can't read it
      </td>
    )
  if (!data)
    return (
      <td colSpan={3} className="text-xs text-neutral-400">
        Reading…
      </td>
    )
  const cell = (u: Usage, fmt: (n: number) => string, what: string) => {
    const v = u.used ?? u.requested
    const p = percent(v, u.allocatable)
    const how = u.used === null ? 'requested' : 'used'
    return (
      <td className="min-w-32" title={`${fmt(v)} of ${fmt(u.allocatable)} ${how}`}>
        <div className="mb-1 text-xs text-neutral-500">
          {p}% {how}
        </div>
        <Bar value={p} label={`${what} ${how}`} />
      </td>
    )
  }
  const pods = percent(data.pods.running, data.pods.allocatable)
  return (
    <>
      {cell(data.cpu, (n) => `${cores(n)} cores`, 'CPU')}
      {cell(data.memory, bytes, 'Memory')}
      <td className="min-w-28" title={`${data.pods.running} of ${data.pods.allocatable} pods`}>
        <div className="mb-1 text-xs text-neutral-500">
          {data.pods.running}/{data.pods.allocatable}
        </div>
        <Bar value={pods} label="Pods" />
      </td>
    </>
  )
}
