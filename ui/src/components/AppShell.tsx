// The console's shell: a full-width header (logo, theme, account menu)
// over a grouped sidebar and the page.
import { lazy, Suspense, useCallback, useEffect, useRef, useState, type RefObject } from 'react'
import { Link, NavLink, Outlet, useLocation, useNavigate } from 'react-router-dom'
import { auth, errorMessage } from '../lib/api'
import { download, kubeconfig } from '../lib/format'
import { useShellTarget } from '../lib/kubectlShell'
import { schemaFor } from '../lib/kube'
import { paths, peoplePath } from '../lib/routes'
import { setSession, useSession } from '../lib/session'
import { TYPES } from '../lib/types'
import { ApiTokens } from './ApiTokens'
import { Sessions } from './Sessions'
import { ChangePassword } from './ChangePassword'
import { ErrorBoundary } from './ErrorBoundary'
import { Icon } from './Icon'
import { Logo } from './Logo'
import { ThemeToggle } from './ThemeToggle'
import { EnvironmentPicker } from './EnvironmentPicker'
import { useEnvironmentScoped } from '../lib/environment'
import { ErrorBanner } from './ui'

// type: shown only when the management cluster serves it; explore: only
// when the user may browse the management cluster.
// The kubectl shell brings xterm.js: loaded when first opened.
const KubectlShell = lazy(() => import('./KubectlShell').then((m) => ({ default: m.KubectlShell })))

// type: shown when the cluster serves it; settings: when the user may read
// the host's settings.
// to: a function when where it goes depends on what the cluster serves;
// also: other pages it's highlighted on (a page with tabs).
type Item = { label: string; icon: string; to: string | (() => string); also?: string[]; type?: string; settings?: boolean }

const groups: { label: string; items: Item[] }[] = [
  {
    label: 'Clusters',
    items: [
      { label: 'Overview', icon: 'overview', to: paths.overview },
      { label: 'Clusters', icon: 'clusters', to: paths.clusters, type: TYPES.CLUSTER },
      { label: 'Templates', icon: 'templates', to: paths.templates, type: TYPES.TEMPLATE },
      { label: 'Fleet', icon: 'fleet', to: paths.fleetRoot, type: TYPES.FLEET_HELM_OP },
      { label: 'Groups', icon: 'groups', to: paths.groups, type: TYPES.FLEET_HELM_OP },
    ],
  },
  {
    label: 'Configuration',
    items: [
      { label: 'Providers', icon: 'providers', to: paths.providers },
      { label: 'ClusterClasses', icon: 'classes', to: paths.clusterClasses, type: TYPES.CLUSTER_CLASS },
      { label: 'Environments', icon: 'environments', to: paths.environments },
    ],
  },
  {
    label: 'Administration',
    items: [
      { label: 'People', icon: 'users', to: peoplePath, also: [paths.users, paths.access] },
      { label: 'Settings', icon: 'settings', to: paths.settings, settings: true },
    ],
  },
]

const linkClass = ({ isActive }: { isActive: boolean }) =>
  `flex min-h-11 items-center gap-2.5 rounded-lg px-3 py-2 text-sm font-medium md:min-h-0 no-underline transition-colors hover:no-underline ${
    isActive
      ? 'bg-neutral-900 text-white dark:bg-white dark:text-neutral-900'
      : 'text-neutral-600 hover:bg-neutral-100 dark:text-neutral-400 dark:hover:bg-neutral-800/70'
  }`

function Sidebar({ onNavigate }: { onNavigate?: () => void }) {
  const { canSettings } = useSession()
  const { pathname } = useLocation()
  const visible = groups
    .map((g) => ({ ...g, items: g.items.filter((i) => (!i.type || schemaFor(i.type)) && (!i.settings || canSettings)) }))
    .filter((g) => g.items.length)
  return (
    <nav className="w-full flex-1 space-y-3 overflow-y-auto px-2.5 pb-[max(0.75rem,env(safe-area-inset-bottom))]">
      {visible.map((g) => (
        <div key={g.label}>
          <div className="px-3 pt-4 pb-1 text-[11px] font-semibold tracking-wider text-neutral-400 uppercase dark:text-neutral-600">{g.label}</div>
          <div className="space-y-0.5">
            {g.items.map((item) => (
              <NavLink
                key={item.label}
                to={typeof item.to === 'function' ? item.to() : item.to}
                className={({ isActive }) => linkClass({ isActive: isActive || !!item.also?.includes(pathname) })}
                onClick={onNavigate}
              >
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
  const [showingTokens, setShowingTokens] = useState(false)
  const [showingSessions, setShowingSessions] = useState(false)

  useEffect(() => {
    if (!open) return
    const onKey = (e: KeyboardEvent) => e.key === 'Escape' && setOpen(false)
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [open])

  if (!user) return null
  const item =
    'min-h-11 w-full rounded-lg px-2.5 py-1.5 text-left text-sm font-medium md:min-h-0 text-neutral-500 transition-colors hover:bg-neutral-100 hover:text-neutral-900 dark:hover:bg-neutral-700/70 dark:hover:text-neutral-100'

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
          <div className="absolute right-0 z-50 mt-2 w-64 max-w-[calc(100vw-2rem)] rounded-xl border border-neutral-200 bg-white p-3 shadow-lg ring-1 ring-black/5 dark:border-neutral-700 dark:bg-neutral-800 dark:ring-white/10">
            <div className="flex items-center justify-between">
              <span className="truncate text-sm font-medium text-neutral-900 dark:text-neutral-100">{user.username}</span>
              {user.groups?.length > 0 && (
                <span className="shrink-0 rounded-full bg-neutral-100 px-2 py-0.5 text-xs font-medium text-neutral-600 dark:bg-neutral-700 dark:text-neutral-300">
                  {user.groups[0]}
                </span>
              )}
            </div>
            <span className="mt-0.5 block truncate text-xs text-neutral-500">hyve {version}</span>
            <div className="mt-3 border-t border-neutral-100 pt-3 lg:hidden dark:border-neutral-700">
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
                title="Tokens that sign in as you, for scripts and CI"
                onClick={() => {
                  setOpen(false)
                  setShowingTokens(true)
                }}
              >
                API tokens
              </button>
              <button
                type="button"
                className={item}
                title="Where you're signed in; sign out other browsers and CLIs"
                onClick={() => {
                  setOpen(false)
                  setShowingSessions(true)
                }}
              >
                Sessions
              </button>
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
      {showingTokens && <ApiTokens user={user.username} own onClose={() => setShowingTokens(false)} />}
      {showingSessions && <Sessions user={user.username} own onClose={() => setShowingSessions(false)} />}
    </div>
  )
}

// The sidebar below lg (phones and tablets): a drawer over the page,
// from the header's menu button. It closes on a link, Escape, or a tap
// beside it; focus goes into it on opening, stays in it, and returns to the menu button on closing.
function Drawer({ onClose, opener }: { onClose: () => void; opener: RefObject<HTMLButtonElement | null> }) {
  const panel = useRef<HTMLDivElement>(null)
  useEffect(() => {
    const el = panel.current
    const back = opener.current
    el?.querySelector<HTMLElement>('button, a')?.focus()
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose()
      if (e.key !== 'Tab' || !el) return
      const all = el.querySelectorAll<HTMLElement>('button, a[href]')
      const first = all[0]
      const last = all[all.length - 1]
      if (e.shiftKey && document.activeElement === first) {
        e.preventDefault()
        last?.focus()
      } else if (!e.shiftKey && document.activeElement === last) {
        e.preventDefault()
        first?.focus()
      }
    }
    window.addEventListener('keydown', onKey)
    const root = document.documentElement
    const was = root.style.overflow
    root.style.overflow = 'hidden'
    return () => {
      window.removeEventListener('keydown', onKey)
      root.style.overflow = was
      back?.focus()
    }
  }, [onClose, opener])
  return (
    <div className="fixed inset-0 z-40 lg:hidden">
      <button type="button" aria-label="Close menu" tabIndex={-1} className="absolute inset-0 bg-black/40" onClick={onClose} />
      <div
        ref={panel}
        role="dialog"
        aria-modal="true"
        aria-label="Menu"
        className="absolute inset-y-0 left-0 flex w-72 max-w-[85vw] flex-col overflow-y-auto border-r border-neutral-200 bg-white pt-[env(safe-area-inset-top)] pl-[env(safe-area-inset-left)] shadow-xl dark:border-neutral-800 dark:bg-neutral-900"
      >
        <div className="flex items-center justify-between p-2 pl-3">
          <Logo />
          <button
            type="button"
            aria-label="Close menu"
            className="flex h-11 w-11 items-center justify-center rounded-lg text-neutral-500 hover:bg-neutral-100 dark:hover:bg-neutral-800"
            onClick={onClose}
          >
            <Icon name="close" />
          </button>
        </div>
        <Sidebar onNavigate={onClose} />
      </div>
    </div>
  )
}

export function AppShell() {
  const [mobileOpen, setMobileOpen] = useState(false)
  const scoped = useEnvironmentScoped()
  const menuButton = useRef<HTMLButtonElement>(null)
  // Stable, so the drawer's focus handling runs once per opening.
  const closeMenu = useCallback(() => setMobileOpen(false), [])
  const [error, setError] = useState('')
  const location = useLocation()
  const shell = useShellTarget()
  useEffect(() => {
    setMobileOpen(false)
    setError('')
  }, [location.pathname])
  // Wide enough for the sidebar, the drawer (and its scroll lock) goes.
  useEffect(() => {
    const wide = window.matchMedia('(width >= 64rem)')
    const onChange = () => wide.matches && setMobileOpen(false)
    wide.addEventListener('change', onChange)
    return () => wide.removeEventListener('change', onChange)
  }, [])

  // Installed (standalone), the page runs under the status bar and beside
  // a landscape notch: the header and main area pad by the safe-area
  // insets (0 in a browser tab).
  return (
    <div className="flex min-h-dvh flex-col bg-neutral-50 dark:bg-neutral-950">
      <header className="sticky top-0 z-30 flex h-[var(--app-header)] shrink-0 items-center justify-between border-b border-neutral-200 bg-white pt-[env(safe-area-inset-top)] pr-[max(1rem,env(safe-area-inset-right))] pl-[max(1rem,env(safe-area-inset-left))] sm:pr-[max(1.5rem,env(safe-area-inset-right))] sm:pl-[max(1.5rem,env(safe-area-inset-left))] dark:border-neutral-800 dark:bg-neutral-900">
        <div className="flex items-center gap-1">
          <button
            ref={menuButton}
            type="button"
            aria-label="Open menu"
            aria-expanded={mobileOpen}
            onClick={() => setMobileOpen(true)}
            className="-ml-2.5 flex h-11 w-11 items-center justify-center rounded-lg text-neutral-600 hover:bg-neutral-100 lg:hidden dark:text-neutral-400 dark:hover:bg-neutral-800"
          >
            <Icon name="menu" size={22} />
          </button>
          <Link to="/" className="flex items-center" aria-label="hyve home">
            <Logo />
          </Link>
        </div>
        <div className="flex min-w-0 items-center gap-3 sm:gap-4">
          {scoped && <EnvironmentPicker />}
          <div className="hidden lg:block">
            <ThemeToggle />
          </div>
          <div className="hidden h-6 w-px bg-neutral-200 lg:block dark:bg-neutral-800" />
          <UserMenu onError={setError} />
        </div>
      </header>

      {mobileOpen && <Drawer opener={menuButton} onClose={closeMenu} />}

      <div className="flex min-h-0 flex-1">
        <aside className="sticky top-[var(--app-header)] hidden h-[calc(100dvh-var(--app-header))] w-52 shrink-0 border-r border-neutral-200 bg-white pl-[env(safe-area-inset-left)] lg:flex xl:w-60 dark:border-neutral-800 dark:bg-neutral-900">
          <Sidebar />
        </aside>
        <main
          className={`min-w-0 flex-1 py-5 pr-[max(1rem,env(safe-area-inset-right))] pl-[max(1rem,env(safe-area-inset-left))] sm:py-6 sm:pr-6 sm:pl-6 lg:px-8 ${shell ? 'pb-[calc(40vh+4rem)]' : 'pb-[max(1.25rem,env(safe-area-inset-bottom))]'}`}
        >
          {/* The explorer and YAML pages use a wide screen's width. */}
          <div className={`mx-auto ${/\/explorer$|^\/yaml\//.test(location.pathname) ? 'max-w-[1600px]' : 'max-w-6xl'}`}>
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
