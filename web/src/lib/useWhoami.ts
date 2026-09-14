import { useEffect, useState, useSyncExternalStore } from 'react'
import { whoami, type Whoami } from './api/auth'
import { useSession } from './useAuth'
import { getActAsNamespace, subscribe as subscribeActAs } from './actAsStore'

/**
 * Fetches the caller's identity/role — role-aware UI gating reads from this
 * rather than decoding the access token client-side (the token payload
 * carries no role at all, see internal/api/token.go). Refetches on every
 * "Viewing" change, not just once per session: Namespace/ReconcilingCluster/
 * Migrating all mirror whichever organization Server.TenantNamespace
 * currently resolves the caller's requests to (internal/api/whoami_handler.go),
 * which for a superadmin follows the X-Hyve-Act-As-Namespace header
 * (apiFetch) — without this dependency, AppShell's own Settings nav-gating
 * (Boolean(who?.reconcilingCluster)) and every org-scoped page reading
 * who.namespace/who.reconcilingCluster stayed stuck on whichever
 * organization was active when the session first loaded. Confirmed live:
 * switching "Viewing" from the control plane to a tenant organization left
 * Settings mis-gated until a full page reload.
 */
export function useWhoami(): { data: Whoami | null; loading: boolean } {
  const session = useSession()
  const actAs = useSyncExternalStore(subscribeActAs, getActAsNamespace)
  const [data, setData] = useState<Whoami | null>(null)
  const [loading, setLoading] = useState(false)

  useEffect(() => {
    if (!session) {
      setData(null)
      return
    }
    let cancelled = false
    setLoading(true)
    whoami()
      .then((res) => {
        if (!cancelled) setData(res)
      })
      .catch(() => {
        if (!cancelled) setData(null)
      })
      .finally(() => {
        if (!cancelled) setLoading(false)
      })
    return () => {
      cancelled = true
    }
  }, [session?.username, actAs])

  return { data, loading }
}
