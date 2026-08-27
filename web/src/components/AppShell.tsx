import { NavLink, Outlet } from 'react-router-dom'
import { logout } from '../lib/api/auth'
import { useSession } from '../lib/useAuth'
import { useWhoami } from '../lib/useWhoami'

const navItems = [
  { to: '/clusters', label: 'Clusters' },
  { to: '/templates', label: 'Templates' },
  { to: '/workflows', label: 'Workflows' },
  { to: '/resources', label: 'Resources' },
  { to: '/modules', label: 'Modules' },
  { to: '/secrets', label: 'Secrets' },
]

const linkClass = ({ isActive }: { isActive: boolean }) =>
  `block rounded px-3 py-1.5 text-sm font-medium ${
    isActive
      ? 'bg-neutral-900 text-white dark:bg-neutral-100 dark:text-neutral-900'
      : 'text-neutral-600 hover:bg-neutral-100 dark:text-neutral-400 dark:hover:bg-neutral-800'
  }`

export function AppShell() {
  const who = useWhoami().data
  useSession() // re-renders this component on login/logout

  return (
    <div className="flex min-h-screen bg-neutral-50 dark:bg-neutral-950">
      <aside className="flex w-56 shrink-0 flex-col border-r border-neutral-200 bg-white dark:border-neutral-800 dark:bg-neutral-900">
        <div className="border-b border-neutral-200 px-4 py-3 dark:border-neutral-800">
          <span className="text-sm font-semibold text-neutral-900 dark:text-neutral-100">hyve console</span>
        </div>
        <nav className="flex-1 space-y-1 p-2">
          {navItems.map((item) => (
            <NavLink key={item.to} to={item.to} className={linkClass}>
              {item.label}
            </NavLink>
          ))}
        </nav>
        <div className="border-t border-neutral-200 p-3 text-sm dark:border-neutral-800">
          {who && (
            <div className="mb-2">
              <div className="font-medium text-neutral-900 dark:text-neutral-100">{who.username}</div>
              <span className="inline-block rounded bg-neutral-100 px-1.5 py-0.5 text-xs text-neutral-600 dark:bg-neutral-800 dark:text-neutral-400">
                {who.role}
              </span>
            </div>
          )}
          <button
            type="button"
            onClick={() => logout()}
            className="w-full rounded px-2 py-1 text-left text-xs font-medium text-neutral-600 hover:bg-neutral-100 dark:text-neutral-400 dark:hover:bg-neutral-800"
          >
            Sign out
          </button>
        </div>
      </aside>
      <main className="flex-1 overflow-auto px-6 py-6">
        <Outlet />
      </main>
    </div>
  )
}
