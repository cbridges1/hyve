import { auth, LOCAL, request } from './api'
import { loadSchemas } from './kube'
import { startLive } from './live'
import { setSession } from './session'

// Signed in: who, and what the cluster serves.
export async function startSession() {
  const me = await auth.me()
  setSession({ user: me, version: me.version })
  const [, canExplore] = await Promise.all([loadSchemas(), mayList('namespaces')])
  setSession({ canExplore })
  startLive()
}

// Whether the user may list a kind across the management cluster.
async function mayList(resource: string): Promise<boolean> {
  try {
    const r = await request('POST', `${LOCAL}/apis/authorization.k8s.io/v1/selfsubjectaccessreviews`, {
      apiVersion: 'authorization.k8s.io/v1',
      kind: 'SelfSubjectAccessReview',
      spec: { resourceAttributes: { verb: 'list', resource } },
    })
    return !!r?.status?.allowed
  } catch {
    return false
  }
}
