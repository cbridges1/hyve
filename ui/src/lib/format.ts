import { dump } from 'js-yaml'
import { request } from './api'
import type { Obj } from './kube'
import { cliServer } from './session'
import { NEVER } from './types'

// How long ago an RFC 3339 time was, as kubectl shows it.
export function age(ts?: string): string {
  if (!ts) return ''
  const s = Math.max(0, Math.floor((Date.now() - Date.parse(ts)) / 1000))
  if (s < 120) return `${s}s`
  if (s < 7200) return `${Math.floor(s / 60)}m`
  if (s < 172800) return `${Math.floor(s / 3600)}h`
  return `${Math.floor(s / 86400)}d`
}

export type Tone = 'good' | 'busy' | 'bad' | 'idle' | 'env' | 'info'
export type Status = { label: string; tone: Tone; title?: string }

// How the console shows a CAPI Cluster's state.
const PHASES: Record<string, Status> = {
  Provisioned: { label: 'Active', tone: 'good' },
  Provisioning: { label: 'Creating', tone: 'busy' },
  Pending: { label: 'Pending', tone: 'busy' },
  Deleting: { label: 'Deleting', tone: 'busy' },
  Failed: { label: 'Failed', tone: 'bad' },
}

export function clusterStatus(c: Obj): Status {
  if (c.metadata.deletionTimestamp) return { label: 'Deleting', tone: 'busy' }
  const p = PHASES[c.status?.phase]
  const notReady = (c.status?.conditions || []).find((x: any) => x.type === 'Available' && x.status === 'False')
  return p ? { ...p, title: notReady?.message || '' } : { label: c.status?.phase || 'Unknown', tone: 'idle' }
}

function duration(ms: number): string {
  const minutes = Math.ceil(ms / 60000)
  if (minutes < 60) return `${minutes}m`
  const hours = Math.floor(minutes / 60)
  return hours < 24 ? `${hours}h ${minutes % 60}m` : `${Math.floor(hours / 24)}d ${hours % 24}h`
}

// "Expires in 3h 20m", or null for a cluster that never expires.
export function expiry(at?: string): Status | null {
  if (!at || at === NEVER) return null
  const t = Date.parse(at)
  if (Number.isNaN(t)) return null
  const ms = t - Date.now()
  const title = `Scheduled for deletion at ${new Date(t).toLocaleString()}`
  return ms <= 0 ? { label: 'Expired — deleting', tone: 'busy', title } : { label: `Expires in ${duration(ms)}`, tone: 'idle', title }
}

// A condition's status ("True", "False", "Unknown"), or undefined.
export function condition(o: Obj, type: string): any {
  return (o.status?.conditions || []).find((c: any) => c.type === type)
}

// An object as YAML, without what nobody edits.
export function toYaml(o: unknown, opts: { dropStatus?: boolean } = {}): string {
  const c = JSON.parse(JSON.stringify(o))
  if (c?.metadata) delete c.metadata.managedFields
  if (opts.dropStatus) delete c.status
  return dump(c, { lineWidth: 120, noRefs: true })
}

// A kubeconfig for a cluster through the host's proxy, as 'hyve
// kubeconfig' and 'hyve cluster kubeconfig' print it: it holds no secret,
// and runs 'hyve token' for the CLI's session with this host. id is
// "local" (the management cluster) or "<environment>.<name>".
export async function kubeconfig(id: string): Promise<string> {
  const server = cliServer()
  let ca = ''
  try {
    ca = (await request('GET', '/v3/settings/cacerts'))?.value || ''
  } catch {
    // A publicly trusted host has no CA to pin.
  }
  const hostname = new URL(server.url).hostname
  const name = id === 'local' ? hostname : `${hostname}-${id}`
  const cluster: Record<string, string> = { server: `${server.url}/k8s/clusters/${id}` }
  if (ca) cluster['certificate-authority-data'] = btoa(ca)
  return dump(
    {
      apiVersion: 'v1',
      kind: 'Config',
      clusters: [{ name, cluster }],
      users: [
        {
          name: hostname,
          user: {
            exec: {
              apiVersion: 'client.authentication.k8s.io/v1',
              command: 'hyve',
              args: ['token', '--server', server.name],
              interactiveMode: 'IfAvailable',
              provideClusterInfo: false,
              installHint: 'hyve is needed to use this kubeconfig: https://github.com/cbridges1/hyve',
            },
          },
        },
      ],
      contexts: [{ name, context: { cluster: name, user: hostname } }],
      'current-context': name,
    },
    { lineWidth: -1 },
  )
}

// Saves text as a file.
export function download(filename: string, text: string, type = 'application/yaml') {
  const a = document.createElement('a')
  a.href = URL.createObjectURL(new Blob([text], { type }))
  a.download = filename
  document.body.appendChild(a)
  a.click()
  setTimeout(() => {
    URL.revokeObjectURL(a.href)
    a.remove()
  }, 0)
}
