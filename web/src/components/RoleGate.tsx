import type { ReactNode } from 'react'
import { RoleAdmin, RoleSuperadmin } from '../lib/api/auth'
import { useWhoami } from '../lib/useWhoami'

/**
 * Renders children only for an admin caller. Every mutation (create/delete
 * on clusters/templates/workflows/resources, and secret writes) is gated
 * RoleAdmin server-side (see internal/api's RequireRole calls) — this hides
 * the control instead of letting a read-only caller click through to a 403.
 */
export function AdminOnly({ children }: { children: ReactNode }) {
  const { data: who } = useWhoami()
  if (who?.role !== RoleAdmin) return null
  return <>{children}</>
}

/**
 * Renders children only for a superadmin caller — the one cluster-scoped
 * role that spans tenant namespaces (see POST/GET /environments and
 * PrimaryClusterProvider's access.method: primary gate). An ordinary
 * tenant admin, even of their own namespace, is not a superadmin.
 */
export function SuperadminOnly({ children }: { children: ReactNode }) {
  const { data: who } = useWhoami()
  if (who?.role !== RoleSuperadmin) return null
  return <>{children}</>
}
