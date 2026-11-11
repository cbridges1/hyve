// Who can reach which clusters, as 'hyve access list' reads it: a
// ClusterAccess in an environment (a role on one cluster or all of them),
// environment membership (RoleBindings labelled hyve.io/env-member in
// the environment and the template namespace), or deploying to templates
// (a RoleBinding labelled hyve.io/deployer in the template namespace). Read by the Access page and,
// per user, by Users.
import { idOf, type Obj, type Reader } from './kube'
import { LABELS, TYPES } from './types'

export const ENV_MEMBER = 'hyve.io/env-member'
// Deployers ('hyve access grant --role deployer'): a RoleBinding of
// hyve-deployer in the template namespace, so CI can make and change the
// Deployments aimed at templates.
export const DEPLOYER = 'hyve.io/deployer'

export type Grant = {
  id: string
  name: string
  ns: string
  kind: 'user' | 'group'
  who: string
  subject: string
  role: string
  on: string
  ready: string
  model: Obj
  type: string
}

// readable: whether the grants could be listed (no grants otherwise, rather
// than none granted).
export type AccessData = { envs: string[]; grants: Grant[]; readable: boolean; clusters: Obj[]; users: Obj[] }

export async function loadAccess(r: Reader): Promise<AccessData> {
  const none = () => [] as Obj[]
  let readable = true
  const [nss, cas, rbs, clusters, users] = await Promise.all([
    r.findAll(TYPES.NAMESPACE, { force: true }).catch(none),
    r.findAll(TYPES.CLUSTER_ACCESS, { force: true }).catch(() => {
      readable = false
      return none()
    }),
    r.findAll(TYPES.ROLE_BINDING, { force: true }).catch(none),
    r.findAll(TYPES.CLUSTER, { force: true }).catch(none),
    r.findAll(TYPES.USER, { force: true }).catch(none),
  ])
  const envs = nss
    .filter((n) => n.metadata.labels?.[LABELS.ENVIRONMENT] === 'true')
    .map((n) => n.metadata.name)
    .sort()
  const envSet = new Set(envs)
  const grants: Grant[] = cas.map((ca) => {
    const kind = ca.spec?.group ? 'group' : 'user'
    const who: string = ca.spec?.group || ca.spec?.user || ''
    return {
      id: `ca:${idOf(ca)}`,
      name: ca.metadata.name,
      ns: ca.metadata.namespace || '',
      kind,
      who,
      subject: `${kind} ${who}`,
      role: ca.spec?.role,
      on: ca.spec?.clusterName || 'every cluster',
      ready: (ca.status?.conditions || []).find((c: any) => c.type === 'Ready')?.status || '-',
      model: ca,
      type: TYPES.CLUSTER_ACCESS,
    }
  })
  rbs
    .filter((rb) => rb.metadata.labels?.[ENV_MEMBER] === 'true' && envSet.has(rb.metadata.namespace || ''))
    .forEach((rb) => {
      ;(rb.subjects || []).forEach((s: any) => {
        const kind = String(s.kind).toLowerCase() === 'group' ? 'group' : 'user'
        const who = String(s.name).replace(/^hyve:/, '')
        grants.push({
          id: `rb:${idOf(rb)}:${s.name}`,
          name: rb.metadata.name,
          ns: rb.metadata.namespace || '',
          kind,
          who,
          subject: `${kind} ${who}`,
          role: 'environment member',
          on: rb.metadata.namespace || '',
          ready: '-',
          model: rb,
          type: TYPES.ROLE_BINDING,
        })
      })
    })
  rbs
    .filter((rb) => rb.metadata.labels?.[DEPLOYER] === 'true')
    .forEach((rb) => {
      ;(rb.subjects || []).forEach((s: any) => {
        const kind = String(s.kind).toLowerCase() === 'group' ? 'group' : 'user'
        const who = String(s.name).replace(/^hyve:/, '')
        grants.push({
          id: `dep:${idOf(rb)}:${s.name}`,
          name: rb.metadata.name,
          ns: rb.metadata.namespace || '',
          kind,
          who,
          subject: `${kind} ${who}`,
          role: 'deployer',
          on: `Deployments in ${rb.metadata.namespace}`,
          ready: '-',
          model: rb,
          type: TYPES.ROLE_BINDING,
        })
      })
    })
  grants.sort((a, b) => `${a.ns}/${a.name}`.localeCompare(`${b.ns}/${b.name}`))
  const sorted = [...users].sort((a, b) => a.metadata.name.localeCompare(b.metadata.name))
  return { envs, grants, readable, clusters, users: sorted }
}

// A user's grants: their own, then their groups'.
export function grantsFor(grants: Grant[], user: string, groups: string[] = []): Grant[] {
  const g = new Set(groups)
  const own = grants.filter((x) => x.kind === 'user' && x.who === user)
  return [...own, ...grants.filter((x) => x.kind === 'group' && g.has(x.who))]
}

// One grant in a few words: "owner of web in prod", "read-only on every
// cluster in dev", "member of environment dev".
export function describeGrant(g: Grant): string {
  if (g.role === 'deployer') return `deploys to templates (${g.ns})`
  if (g.type === TYPES.ROLE_BINDING) return `member of environment ${g.ns}`
  return g.on === 'every cluster' ? `${g.role} on every cluster in ${g.ns}` : `${g.role} of ${g.on} in ${g.ns}`
}
