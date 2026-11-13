// The app's own state, over every page (sign-in too): offline, the host
// not answering, and a new build waiting to load (lib/pwa.ts). At the bottom, above an installed
// app's home bar.
import { useEffect, useRef } from 'react'
import { applyUpdate, usePWA } from '../lib/pwa'
import { useSession } from '../lib/session'

export function AppStatus() {
  const { online, waiting, unreachable } = usePWA()
  const { user } = useSession()
  // Offline, the stored shell starts but can't learn who's signed in:
  // back online, start again rather than stay on the sign-in page.
  const wentOffline = useRef(!online)
  useEffect(() => {
    if (!online) wentOffline.current = true
    else if (wentOffline.current && !user) window.location.reload()
  }, [online, user])
  if (online && !waiting && !unreachable) return null
  return (
    <div className="pointer-events-none fixed inset-x-0 bottom-0 z-[60] flex flex-col items-center gap-2 px-4 pb-[max(1rem,env(safe-area-inset-bottom))]">
      {!online && (
        <div role="status" className="pointer-events-auto flex w-full max-w-md items-center gap-3 rounded-xl bg-neutral-900 px-4 py-3 text-sm text-white shadow-lg dark:bg-white dark:text-neutral-900">
          <span className="h-2 w-2 shrink-0 rounded-full bg-amber-400" />
          <span className="flex-1">You're offline. hyve shows nothing until it reaches its host again.</span>
        </div>
      )}
      {online && unreachable && (
        <div role="status" className="pointer-events-auto flex w-full max-w-md items-center gap-3 rounded-xl bg-neutral-900 px-4 py-3 text-sm text-white shadow-lg dark:bg-white dark:text-neutral-900">
          <span className="h-2 w-2 shrink-0 rounded-full bg-amber-400" />
          <span className="flex-1">Can't reach hyve's host.</span>
          <button type="button" className="btn btn-sm h-9 bg-white text-neutral-900 hover:bg-neutral-200 dark:bg-neutral-900 dark:text-white dark:hover:bg-neutral-800" onClick={() => window.location.reload()}>
            Retry
          </button>
        </div>
      )}
      {waiting && (
        <div role="status" className="pointer-events-auto flex w-full max-w-md items-center gap-3 rounded-xl bg-neutral-900 px-4 py-3 text-sm text-white shadow-lg dark:bg-white dark:text-neutral-900">
          <span className="flex-1">A new version of hyve is ready.</span>
          <button type="button" className="btn btn-sm h-9 bg-white text-neutral-900 hover:bg-neutral-200 dark:bg-neutral-900 dark:text-white dark:hover:bg-neutral-800" onClick={applyUpdate}>
            Reload
          </button>
        </div>
      )}
    </div>
  )
}
