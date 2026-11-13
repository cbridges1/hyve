// A user's signed-in sessions (`hyve user session`): where and when each
// started and was last used, to sign out one, or every other one. Your
// own come from the account menu, another user's from their row on Users
// (that takes the users/sessions right, which admins have). Signing out
// this browser's own session signs you out here.
import { useCallback, useEffect, useState } from 'react'
import { errorMessage, sessions, type SessionInfo } from '../lib/api'
import { useAction } from '../lib/hooks'
import { Modal } from './Modal'
import { ConfirmButton, EmptyState, ErrorBanner, Loading, Pill } from './ui'
import { Table } from './Table'

const when = (ts?: string) => (ts ? new Date(ts).toLocaleString() : '-')

// What a User-Agent is, in a word or two: a browser and its platform, or
// the CLI.
function clientOf(ua?: string): string {
  if (!ua) return 'Not recorded'
  if (!ua.startsWith('Mozilla/')) return ua.split(' ')[0]
  const browser = /Edg\//.test(ua) ? 'Edge' : /Firefox\//.test(ua) ? 'Firefox' : /Chrome\//.test(ua) ? 'Chrome' : /Safari\//.test(ua) ? 'Safari' : 'Browser'
  const os = /iPhone|iPad/.test(ua) ? 'iOS' : /Android/.test(ua) ? 'Android' : /Mac OS X/.test(ua) ? 'macOS' : /Windows/.test(ua) ? 'Windows' : /Linux/.test(ua) ? 'Linux' : ''
  return os ? `${browser} on ${os}` : browser
}

export function Sessions({ user, own, onClose }: { user: string; own: boolean; onClose: () => void }) {
  const [list, setList] = useState<SessionInfo[] | null>(null)
  const [loadError, setLoadError] = useState('')
  const { busy, error, run } = useAction()

  const load = useCallback(() => {
    sessions.list(own ? undefined : user).then(setList, (e) => setLoadError(errorMessage(e)))
  }, [user, own])
  useEffect(load, [load])

  const others = (list || []).filter((s) => !s.current).length

  return (
    <Modal title={own ? 'Your sessions' : `${user}'s sessions`} onClose={onClose} wide>
      <p className="mb-4 text-sm text-neutral-500">
        Where {own ? "you're" : `${user} is`} signed in: browsers, the CLI and CI jobs. Signing one out ends it at once. API tokens are separate.
      </p>
      <ErrorBanner error={loadError} />
      <ErrorBanner error={error} />
      {list === null && !loadError ? (
        <Loading />
      ) : list && list.length === 0 ? (
        <EmptyState>No sessions.</EmptyState>
      ) : (
        list && (
          <div className="mb-4 overflow-x-auto">
            <Table className="w-full text-sm">
              <thead>
                <tr className="text-left text-xs text-neutral-500 uppercase">
                  <th className="py-2 pr-3 font-medium">Client</th>
                  <th className="py-2 pr-3 font-medium">Started</th>
                  <th className="py-2 pr-3 font-medium">Last used</th>
                  <th className="hidden py-2 pr-3 font-medium sm:table-cell">Expires</th>
                  <th />
                </tr>
              </thead>
              <tbody className="divide-y divide-neutral-100 dark:divide-neutral-800">
                {list.map((s) => (
                  <tr key={s.id}>
                    <td className="py-2 pr-3">
                      <div className="flex flex-wrap items-center gap-1.5 font-medium text-neutral-900 dark:text-neutral-100" title={s.userAgent}>
                        {clientOf(s.userAgent)}
                        {s.current && <Pill tone="good" label="This one" />}
                        {s.kind === 'federated' && <Pill tone="info" label="CI job" />}
                      </div>
                      {s.address && <div className="text-xs text-neutral-500">{s.address}</div>}
                    </td>
                    <td className="py-2 pr-3 whitespace-nowrap">{when(s.createdAt)}</td>
                    <td className="py-2 pr-3 whitespace-nowrap">{s.current ? 'Now' : when(s.lastUsed)}</td>
                    <td className="hidden py-2 pr-3 whitespace-nowrap sm:table-cell">{when(s.expiresAt)}</td>
                    <td className="py-2 text-right">
                      <ConfirmButton
                        label="Sign out"
                        confirm={s.current ? 'Sign out here?' : 'Sign it out?'}
                        disabled={busy}
                        onConfirm={() =>
                          run(async () => {
                            await sessions.revoke(s.user, s.id)
                            // This browser's own: the next request finds it
                            // signed out and shows the sign-in page.
                            if (s.current) window.location.reload()
                            else load()
                          })
                        }
                      />
                    </td>
                  </tr>
                ))}
              </tbody>
            </Table>
          </div>
        )
      )}
      <div className="flex flex-wrap justify-end gap-2">
        <button type="button" className="btn btn-tertiary" onClick={onClose}>
          Close
        </button>
        {others > 0 && (
          <ConfirmButton
            variant="danger"
            label={own ? `Sign out ${others} other${others === 1 ? '' : 's'}` : `Sign out all ${others}`}
            confirm={own ? 'Sign out everywhere else?' : `Sign ${user} out everywhere?`}
            disabled={busy}
            onConfirm={() =>
              run(async () => {
                await sessions.revokeOthers(user)
                load()
              })
            }
          />
        )}
      </div>
    </Modal>
  )
}
