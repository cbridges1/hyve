import { useEffect, useState } from 'react'
import { whoami, type Whoami } from './api/auth'
import { useSession } from './useAuth'

/** Fetches the caller's identity/role once per session — role-aware UI gating reads from this rather than decoding the access token client-side (the token payload carries no role at all, see internal/api/token.go). */
export function useWhoami(): { data: Whoami | null; loading: boolean } {
  const session = useSession()
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
  }, [session?.username])

  return { data, loading }
}
