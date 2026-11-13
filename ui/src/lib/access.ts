// Who can reach which clusters, as 'hyve access list' reads it: a
// ClusterAccess in an environment (a role on one cluster or all of them),
// environment membership (RoleBindings labelled hyve.io/env-member in
// the environment and the template namespace), or deploying to templates
// (a RoleBinding labelled hyve.io/deployer in the template namespace). Read by the Access page and,
// per user, by Users.
import { idOf, kube, newObject, save, type Obj, type Reader } from './kube'
import { DEFAULT_TEMPLATE_NAMESPACE, LABELS, TYPES } from './types'

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

// The roles a grant gives, as 'hyve access grant --role' names them.
export const ROLES = [
  { value: 'owner', label: 'Owner', hint: 'everything on the cluster' },
  { value: 'member', label: 'Member', hint: 'see the cluster, create namespaces' },
  { value: 'read-only', label: 'Read-only', hint: 'read everything but Secrets' },
  { value: 'env-member', label: 'Environment member', hint: 'create and manage clusters in the environment' },
  { value: 'deployer', label: 'Deployer', hint: 'make and change the Deployments aimed at templates (for CI)' },
]

export type GrantRequest = { kind: 'user' | 'group'; subject: string; role: string; env: string; cluster: string }

// grantAccess makes (or updates) one grant, under the names 'hyve access
// grant' gives, so the CLI and UI agree: deployers and environment members
// are RoleBindings, the cluster roles a ClusterAccess in the environment
// (on one cluster, or all of them when cluster is empty).
export async function grantAccess(f: GrantRequest) {
  const subjectKind = f.kind === 'group' ? 'Group' : 'User'
  const name = `${subjectKind}-${f.subject}`.toLowerCase()
  const subjects = [{ apiGroup: 'rbac.authorization.k8s.io', kind: subjectKind, name: `hyve:${f.subject}` }]
  const binding = async (ns: string, rbName: string, label: string, role: string) => {
    const rb =
      (await kube.maybe(TYPES.ROLE_BINDING, ns, rbName)) ||
      newObject(TYPES.ROLE_BINDING, { metadata: { name: rbName, namespace: ns, labels: { [LABELS.MANAGED_BY]: 'hyve', [label]: 'true' } } })
    rb.roleRef = { apiGroup: 'rbac.authorization.k8s.io', kind: 'ClusterRole', name: role }
    rb.subjects = subjects
    await save(TYPES.ROLE_BINDING, rb)
  }
  if (f.role === 'deployer') {
    await binding(DEFAULT_TEMPLATE_NAMESPACE, `hyve-deployer-${name}`, DEPLOYER, 'hyve-deployer')
    return
  }
  if (f.role === 'env-member') {
    await binding(f.env, `hyve-member-${name}`, ENV_MEMBER, 'hyve-environment-member')
    await binding(DEFAULT_TEMPLATE_NAMESPACE, `hyve-member-${name}`, ENV_MEMBER, 'hyve-template-reader')
    return
  }
  const spec: Record<string, string> = { role: f.role, [f.kind === 'group' ? 'group' : 'user']: f.subject }
  let caName = `${f.role}-${name}`
  if (f.cluster) {
    spec.clusterName = f.cluster
    caName = `${f.cluster}-${caName}`
  }
  const ca = (await kube.maybe(TYPES.CLUSTER_ACCESS, f.env, caName)) || newObject(TYPES.CLUSTER_ACCESS, { metadata: { name: caName, namespace: f.env } })
  ca.spec = spec
  await save(TYPES.CLUSTER_ACCESS, ca)
}
