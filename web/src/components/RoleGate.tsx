import type { ReactNode } from 'react'
import { RoleAdmin, RoleSuperadmin } from '../lib/api/auth'
import { useWhoami } from '../lib/useWhoami'

/**
 * Renders children for an admin OR superadmin caller. Every mutation
 * (create/delete/update on clusters/templates/workflows/resources/access
 * methods, secret writes, workflow runs) is gated RoleAdmin server-side —
 * server.go's RequireRole treats a superadmin as satisfying any RoleAdmin
 * gate (see its own doc comment), so this must mirror that exactly, not
 * just check RoleAdmin — confirmed live: without the RoleSuperadmin branch
 * here, a superadmin could already do all of this via the API, but every
 * button/panel for it stayed invisible in the console regardless of which
 * environment they were "Viewing".
 */
export function AdminOnly({ children }: { children: ReactNode }) {
  const { data: who } = useWhoami()
  if (who?.role !== RoleAdmin && who?.role !== RoleSuperadmin) return null
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
