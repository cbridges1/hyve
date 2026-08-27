import type { ReactNode } from 'react'
import { RoleAdmin } from '../lib/api/auth'
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
