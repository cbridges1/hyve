// The console's shell: a full-width header (logo, theme, account menu)
// over a grouped sidebar and the page.
import { lazy, Suspense, useEffect, useState } from 'react'
import { Link, NavLink, Outlet, useLocation, useNavigate } from 'react-router-dom'
import { auth, errorMessage } from '../lib/api'
import { download, kubeconfig } from '../lib/format'
import { useShellTarget } from '../lib/kubectlShell'
import { schemaFor } from '../lib/kube'
import { paths } from '../lib/routes'
import { setSession, useSession } from '../lib/session'
import { TYPES } from '../lib/types'
import { ChangePassword } from './ChangePassword'
import { ErrorBoundary } from './ErrorBoundary'
import { Icon } from './Icon'
import { Logo } from './Logo'
import { ThemeToggle } from './ThemeToggle'
import { ErrorBanner } from './ui'

// type: shown only when the management cluster serves it; explore: only
// when the user may browse the management cluster.
// The kubectl shell brings xterm.js: loaded when first opened.
const KubectlShell = lazy(() => import('./KubectlShell').then((m) => ({ default: m.KubectlShell })))

type Item = { label: string; icon: string; to: string; type?: string; explore?: boolean }

const groups: { label: string; items: Item[] }[] = [
  {
    label: 'Clusters',
    items: [
      { label: 'Overview', icon: 'overview', to: paths.overview },
      { label: 'Clusters', icon: 'clusters', to: paths.clusters, type: TYPES.CLUSTER },
      { label: 'Templates', icon: 'templates', to: paths.templates, type: TYPES.TEMPLATE },
    ],
  },
  {
    label: 'Configuration',
    items: [
      { label: 'Resources', icon: 'resources', to: paths.resources, type: TYPES.RESOURCE },
      { label: 'Delivery', icon: 'matrix', to: paths.matrix },
      { label: 'Secrets', icon: 'secrets', to: paths.secrets },
      { label: 'Repos', icon: 'repos', to: paths.repos },
      { label: 'Providers', icon: 'providers', to: paths.providers },
    ],
  },
  {
    label: 'Administration',
    items: [
      { label: 'Users', icon: 'users', to: paths.users, type: TYPES.USER },
      { label: 'Environments', icon: 'environments', to: paths.environments },
      { label: 'Access', icon: 'access', to: paths.access },
      { label: 'Explorer', icon: 'search', to: paths.explorer, explore: true },
    ],
  },
]

const linkClass = ({ isActive }: { isActive: boolean }) =>
  `flex items-center gap-2.5 rounded-lg px-3 py-2 text-sm font-medium no-underline transition-colors hover:no-underline ${
    isActive
      ? 'bg-neutral-900 text-white dark:bg-white dark:text-neutral-900'
      : 'text-neutral-600 hover:bg-neutral-100 dark:text-neutral-400 dark:hover:bg-neutral-800/70'
  }`

function Sidebar({ onNavigate }: { onNavigate?: () => void }) {
  const { canExplore } = useSession()
  const visible = groups
    .map((g) => ({ ...g, items: g.items.filter((i) => (!i.type || schemaFor(i.type)) && (!i.explore || canExplore)) }))
    .filter((g) => g.items.length)
  return (
    <nav className="w-full flex-1 space-y-3 overflow-y-auto px-2.5 pb-3">
      {visible.map((g) => (
        <div key={g.label}>
          <div className="px-3 pt-4 pb-1 text-[11px] font-semibold tracking-wider text-neutral-400 uppercase dark:text-neutral-600">{g.label}</div>
          <div className="space-y-0.5">
            {g.items.map((item) => (
              <NavLink key={item.label} to={item.to} className={linkClass} onClick={onNavigate}>
                <Icon name={item.icon} />
                {item.label}
              </NavLink>
            ))}
          </div>
        </div>
      ))}
    </nav>
  )
}

function UserMenu({ onError }: { onError: (e: string) => void }) {
  const { user, version } = useSession()
  const navigate = useNavigate()
  const [open, setOpen] = useState(false)
  const [changingPassword, setChangingPassword] = useState(false)

  useEffect(() => {
    if (!open) return
    const onKey = (e: KeyboardEvent) => e.key === 'Escape' && setOpen(false)
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [open])

  if (!user) return null
  const item =
    'w-full rounded-lg px-2.5 py-1.5 text-left text-sm font-medium text-neutral-500 transition-colors hover:bg-neutral-100 hover:text-neutral-900 dark:hover:bg-neutral-700/70 dark:hover:text-neutral-100'

  return (
    <div className="relative">
      <button
        type="button"
        aria-expanded={open}
        aria-label="Account menu"
        onClick={() => setOpen((v) => !v)}
        className="flex items-center gap-2 rounded-full py-1 pr-2 pl-1 transition-colors hover:bg-neutral-100 dark:hover:bg-neutral-800"
      >
        <span className="flex h-8 w-8 items-center justify-center rounded-full bg-neutral-900 text-xs font-semibold text-white dark:bg-white dark:text-neutral-900">
          {(user.username || '?').slice(0, 1).toUpperCase()}
        </span>
        <Icon name="chevronDown" size={14} className="text-neutral-400" />
      </button>
      {open && (
        <>
          <button type="button" aria-label="Close menu" className="fixed inset-0 z-40" onClick={() => setOpen(false)} />
          <div className="absolute right-0 z-50 mt-2 w-56 rounded-xl border border-neutral-200 bg-white p-3 shadow-lg ring-1 ring-black/5 dark:border-neutral-700 dark:bg-neutral-800 dark:ring-white/10">
            <div className="flex items-center justify-between">
              <span className="truncate text-sm font-medium text-neutral-900 dark:text-neutral-100">{user.username}</span>
              {user.groups?.length > 0 && (
                <span className="shrink-0 rounded-full bg-neutral-100 px-2 py-0.5 text-xs font-medium text-neutral-600 dark:bg-neutral-700 dark:text-neutral-300">
                  {user.groups[0]}
                </span>
              )}
            </div>
            <span className="mt-0.5 block truncate text-xs text-neutral-500">hyve {version}</span>
            <div className="mt-3 border-t border-neutral-100 pt-3 md:hidden dark:border-neutral-700">
              <span className="mb-1.5 block text-xs font-medium text-neutral-500">Theme</span>
              <ThemeToggle />
            </div>
            <div className="mt-3">
              {/* Local mode signs the browser in by itself; its admin's
                  password is the CLI's to keep. */}
              {!user.autoSignIn && (
                <button
                  type="button"
                  className={item}
                  onClick={() => {
                    setOpen(false)
                    setChangingPassword(true)
                  }}
                >
                  Change password
                </button>
              )}
              <button
                type="button"
                className={item}
                title="A kubeconfig for the management cluster that runs 'hyve token'; it holds no secret"
                onClick={async () => {
                  setOpen(false)
                  try {
                    download('hyve-local.kubeconfig', await kubeconfig('local'))
                  } catch (e) {
                    onError(errorMessage(e))
                  }
                }}
              >
                Download kubeconfig
              </button>
              {!user.autoSignIn && (
                <button
                  type="button"
                  className={item}
                  onClick={async () => {
                    await auth.logout().catch(() => {})
                    setSession({ user: null })
                    navigate('/login')
                  }}
                >
                  Sign out
                </button>
              )}
            </div>
          </div>
        </>
      )}
      {changingPassword && <ChangePassword username={user.username} onClose={() => setChangingPassword(false)} />}
    </div>
  )
}

export function AppShell() {
  const [mobileOpen, setMobileOpen] = useState(false)
  const [error, setError] = useState('')
  const location = useLocation()
  const shell = useShellTarget()
  useEffect(() => {
    setMobileOpen(false)
    setError('')
  }, [location.pathname])

  return (
    <div className="flex min-h-screen flex-col bg-neutral-50 dark:bg-neutral-950">
      <header className="sticky top-0 z-30 flex h-14 shrink-0 items-center justify-between border-b border-neutral-200 bg-white px-4 sm:px-6 dark:border-neutral-800 dark:bg-neutral-900">
        <div className="flex items-center gap-3">
          <button
            type="button"
            aria-label="Open menu"
            onClick={() => setMobileOpen(true)}
            className="-ml-1.5 rounded-lg p-1.5 text-neutral-600 hover:bg-neutral-100 md:hidden dark:text-neutral-400 dark:hover:bg-neutral-800"
          >
            <Icon name="menu" size={22} />
          </button>
          <Link to="/" className="hidden md:block">
            <Logo />
          </Link>
        </div>
        <div className="flex items-center gap-4">
          <div className="hidden md:block">
            <ThemeToggle />
          </div>
          <div className="hidden h-6 w-px bg-neutral-200 md:block dark:bg-neutral-800" />
          <UserMenu onError={setError} />
        </div>
      </header>

      {mobileOpen && (
        <div className="fixed inset-0 z-40 md:hidden">
          <button type="button" aria-label="Close menu" className="absolute inset-0 bg-black/40" onClick={() => setMobileOpen(false)} />
          <div className="absolute inset-y-0 left-0 w-72 max-w-[85vw] overflow-y-auto border-r border-neutral-200 bg-white shadow-xl dark:border-neutral-800 dark:bg-neutral-900">
            <div className="flex items-center justify-between p-2 pl-3">
              <Logo />
              <button
                type="button"
                aria-label="Close menu"
                className="rounded-lg p-1.5 text-neutral-500 hover:bg-neutral-100 dark:hover:bg-neutral-800"
                onClick={() => setMobileOpen(false)}
              >
                <Icon name="close" />
              </button>
            </div>
            <Sidebar onNavigate={() => setMobileOpen(false)} />
          </div>
        </div>
      )}

      <div className="flex min-h-0 flex-1">
        <aside className="sticky top-14 hidden h-[calc(100vh-3.5rem)] w-60 shrink-0 border-r border-neutral-200 bg-white md:flex dark:border-neutral-800 dark:bg-neutral-900">
          <Sidebar />
        </aside>
        <main className={`min-w-0 flex-1 px-4 py-5 sm:px-6 sm:py-6 lg:px-8 ${shell ? 'pb-[calc(40vh+4rem)]' : ''}`}>
          <div className="mx-auto max-w-6xl">
            <ErrorBanner error={error} />
            <ErrorBoundary resetKey={location.pathname}>
              <Outlet />
            </ErrorBoundary>
          </div>
        </main>
      </div>
      {shell && (
        <Suspense fallback={null}>
          <KubectlShell key={shell.cluster} target={shell} />
        </Suspense>
      )}
    </div>
  )
}
