import { useState } from 'react'
import { NavLink, Outlet } from 'react-router-dom'
import { logout, RoleAdmin } from '../lib/api/auth'
import { useSession } from '../lib/useAuth'
import { useWhoami } from '../lib/useWhoami'
import { Logo } from './Logo'
import { ThemeToggle } from './ThemeToggle'
import {
  AccountsIcon,
  ClustersIcon,
  CloseIcon,
  MenuIcon,
  ModulesIcon,
  ResourcesIcon,
  SecretsIcon,
  TemplatesIcon,
  WorkflowsIcon,
} from './icons'

const navItems = [
  { to: '/clusters', label: 'Clusters', Icon: ClustersIcon },
  { to: '/templates', label: 'Templates', Icon: TemplatesIcon },
  { to: '/workflows', label: 'Workflows', Icon: WorkflowsIcon },
  { to: '/resources', label: 'Resources', Icon: ResourcesIcon },
  { to: '/modules', label: 'Modules', Icon: ModulesIcon },
  { to: '/secrets', label: 'Secrets', Icon: SecretsIcon },
]

const linkClass = ({ isActive }: { isActive: boolean }) =>
  `flex items-center gap-2.5 rounded-lg px-3 py-2 text-sm font-medium transition-colors ${
    isActive
      ? 'bg-neutral-900 text-white dark:bg-white dark:text-neutral-900'
      : 'text-neutral-600 hover:bg-neutral-100 dark:text-neutral-400 dark:hover:bg-neutral-800/70'
  }`

function SidebarContent({ onNavigate }: { onNavigate?: () => void }) {
  const who = useWhoami().data

  return (
    <div className="flex h-full flex-col">
      <div className="flex items-center px-4 py-4">
        <Logo className="h-5" />
      </div>
      <nav className="flex-1 space-y-0.5 overflow-y-auto px-2.5">
        {navItems.map((item) => (
          <NavLink key={item.to} to={item.to} className={linkClass} onClick={onNavigate}>
            <item.Icon />
            {item.label}
          </NavLink>
        ))}
        {who?.role === RoleAdmin && (
          <NavLink to="/accounts" className={linkClass} onClick={onNavigate}>
            <AccountsIcon />
            Accounts
          </NavLink>
        )}
      </nav>
      <div className="border-t border-neutral-200 p-3 dark:border-neutral-800">
        <div className="mb-2">
          <ThemeToggle />
        </div>
        {who && (
          <div className="mb-2 flex items-center justify-between rounded-lg px-2 py-1.5">
            <span className="truncate text-sm font-medium text-neutral-900 dark:text-neutral-100">{who.username}</span>
            <span className="shrink-0 rounded-full bg-neutral-100 px-2 py-0.5 text-xs font-medium text-neutral-600 dark:bg-neutral-800 dark:text-neutral-400">
              {who.role}
            </span>
          </div>
        )}
        <button
          type="button"
          onClick={() => logout()}
          className="w-full rounded-lg px-2.5 py-1.5 text-left text-sm font-medium text-neutral-500 transition-colors hover:bg-neutral-100 hover:text-neutral-900 dark:text-neutral-500 dark:hover:bg-neutral-800/70 dark:hover:text-neutral-100"
        >
          Sign out
        </button>
      </div>
    </div>
  )
}

export function AppShell() {
  useSession() // re-renders this component on login/logout
  const [mobileOpen, setMobileOpen] = useState(false)

  return (
    <div className="min-h-screen bg-neutral-50 dark:bg-neutral-950">
      {/* Mobile top bar — hidden from md up, where the persistent sidebar takes over */}
      <div className="flex items-center justify-between border-b border-neutral-200 bg-white px-4 py-3 md:hidden dark:border-neutral-800 dark:bg-neutral-900">
        <Logo className="h-5" />
        <button
          type="button"
          onClick={() => setMobileOpen(true)}
          aria-label="Open menu"
          className="rounded-lg p-1.5 text-neutral-600 hover:bg-neutral-100 dark:text-neutral-400 dark:hover:bg-neutral-800"
        >
          <MenuIcon width={22} height={22} />
        </button>
      </div>

      {/* Mobile off-canvas drawer */}
      {mobileOpen && (
        <div className="fixed inset-0 z-40 md:hidden">
          <button
            type="button"
            aria-label="Close menu"
            className="absolute inset-0 bg-black/40"
            onClick={() => setMobileOpen(false)}
          />
          <div className="absolute inset-y-0 left-0 w-72 max-w-[85vw] border-r border-neutral-200 bg-white shadow-xl dark:border-neutral-800 dark:bg-neutral-900">
            <div className="flex justify-end p-2">
              <button
                type="button"
                onClick={() => setMobileOpen(false)}
                aria-label="Close menu"
                className="rounded-lg p-1.5 text-neutral-500 hover:bg-neutral-100 dark:hover:bg-neutral-800"
              >
                <CloseIcon />
              </button>
            </div>
            <SidebarContent onNavigate={() => setMobileOpen(false)} />
          </div>
        </div>
      )}

      <div className="flex">
        <aside className="sticky top-0 hidden h-screen w-60 shrink-0 border-r border-neutral-200 bg-white md:flex dark:border-neutral-800 dark:bg-neutral-900">
          <SidebarContent />
        </aside>
        <main className="min-w-0 flex-1 px-4 py-5 sm:px-6 sm:py-6 lg:px-8">
          <div className="mx-auto max-w-6xl">
            <Outlet />
          </div>
        </main>
      </div>
    </div>
  )
}
