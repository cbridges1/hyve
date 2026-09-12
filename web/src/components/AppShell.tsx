import { useEffect, useState } from 'react'
import { NavLink, Outlet } from 'react-router-dom'
import { environmentsApi } from '../lib/api/environments'
import { logout, RoleAdmin, RoleSuperadmin } from '../lib/api/auth'
import { useActAs } from '../lib/useActAs'
import { useApi } from '../lib/useApi'
import { useSession } from '../lib/useAuth'
import { useWhoami } from '../lib/useWhoami'
import { Logo } from './Logo'
import { ThemeToggle } from './ThemeToggle'
import {
  AccountsIcon,
  ChevronDownIcon,
  ClustersIcon,
  CloseIcon,
  EnvironmentsIcon,
  MenuIcon,
  ModulesIcon,
  ResourcesIcon,
  SecretsIcon,
  SettingsIcon,
  TemplatesIcon,
  WorkflowsIcon,
} from './icons'

// Grouped the way the sidebar renders them — each group gets its own small
// uppercase section header, mirroring a typical nested-sidebar dashboard
// layout. Accounts/Environments aren't here: their visibility depends on
// role/actAs state, so they're rendered as their own conditionally-shown
// "Organization" group below instead of being filtered into this list.
const navGroups: { label: string; items: { to: string; label: string; Icon: typeof ClustersIcon }[] }[] = [
  {
    label: 'Clusters',
    items: [
      { to: '/clusters', label: 'Clusters', Icon: ClustersIcon },
      { to: '/templates', label: 'Templates', Icon: TemplatesIcon },
      { to: '/workflows', label: 'Workflows', Icon: WorkflowsIcon },
    ],
  },
  {
    label: 'Configuration',
    items: [
      { to: '/resources', label: 'Resources', Icon: ResourcesIcon },
      { to: '/modules', label: 'Modules', Icon: ModulesIcon },
      { to: '/secrets', label: 'Secrets', Icon: SecretsIcon },
    ],
  },
]

const linkClass = ({ isActive }: { isActive: boolean }) =>
  `flex items-center gap-2.5 rounded-lg px-3 py-2 text-sm font-medium transition-colors ${
    isActive
      ? 'bg-neutral-900 text-white dark:bg-white dark:text-neutral-900'
      : 'text-neutral-600 hover:bg-neutral-100 dark:text-neutral-400 dark:hover:bg-neutral-800/70'
  }`

const groupHeaderClass = 'px-3 pt-4 pb-1 text-[11px] font-semibold tracking-wider text-neutral-400 uppercase dark:text-neutral-600'

// Sits at the very top of the sidebar, the same position a Pangolin-style
// dashboard gives its org switcher — replaces the old placement directly
// under the logo now that the logo itself lives in the header instead.
// Lets a superadmin view/act within a chosen tenant without a separate
// HyveAccessBinding of their own there — see Server.TenantNamespace's own
// doc comment for why the header this drives is the actual mechanism.
// Independent of, and never changes, the real session identity shown in
// the header's own user menu (that's who's actually logged in; this is
// which tenant's data every /api/* request currently resolves against).
function EnvironmentSwitcher() {
  const [actAs, setActAs] = useActAs()
  const { data: environments } = useApi(() => environmentsApi.list())

  return (
    <div className="border-b border-neutral-200 px-2.5 pt-3 pb-3 dark:border-neutral-800">
      <label className="mb-1 block px-0.5 text-[11px] font-semibold tracking-wider text-neutral-400 uppercase dark:text-neutral-600">
        Viewing
      </label>
      <select
        value={actAs ?? ''}
        onChange={(e) => setActAs(e.target.value || null)}
        className="w-full rounded-lg border border-neutral-300 bg-white px-2.5 py-1.5 text-sm dark:border-neutral-700 dark:bg-neutral-800"
      >
        <option value="">Control plane</option>
        {environments?.map((env) => (
          <option key={env.name} value={env.namespace}>
            {env.name}
          </option>
        ))}
      </select>
    </div>
  )
}

// Non-superadmin counterpart to EnvironmentSwitcher above — there's nothing
// to switch (one binding, one namespace), but still gives every role the
// same permanent "which org am I looking at" orientation the switcher gives
// a superadmin, addressed at a fixed spot rather than folded into a menu.
function OrganizationLabel({ namespace }: { namespace: string }) {
  return (
    <div className="border-b border-neutral-200 px-2.5 pt-3 pb-3 dark:border-neutral-800">
      <div className="px-0.5 text-[11px] font-semibold tracking-wider text-neutral-400 uppercase dark:text-neutral-600">
        Organization
      </div>
      <div className="mt-1 truncate px-0.5 text-sm font-medium text-neutral-900 dark:text-neutral-100">{namespace}</div>
    </div>
  )
}

function SidebarContent({ onNavigate }: { onNavigate?: () => void }) {
  const who = useWhoami().data
  const [actAs] = useActAs()

  return (
    <div className="flex h-full flex-col">
      {who?.role === RoleSuperadmin ? (
        <EnvironmentSwitcher />
      ) : (
        who && <OrganizationLabel namespace={who.namespace} />
      )}
      <nav className="flex-1 space-y-3 overflow-y-auto px-2.5 pb-3">
        {navGroups.map((group) => (
          <div key={group.label}>
            <div className={groupHeaderClass}>{group.label}</div>
            <div className="space-y-0.5">
              {group.items.map((item) => (
                <NavLink key={item.to} to={item.to} className={linkClass} onClick={onNavigate}>
                  <item.Icon />
                  {item.label}
                </NavLink>
              ))}
            </div>
          </div>
        ))}
        {/* Accounts always follows whichever environment "Viewing" is
            currently set to (see Server.TenantNamespace) — a superadmin
            managing hyve-system's own accounts is just "Viewing: Control
            plane" + Accounts, the same page a tenant admin already uses,
            not a separate mechanism. Environments (creating/listing
            tenants) and Settings (the install-wide HyveConfig singleton)
            are both control-plane-only — neither is scoped to any one
            tenant, so both only make sense while Viewing: Control plane,
            unlike Accounts above. Grouped under its own header only when
            at least one of the three is actually visible, so this
            role-gated group never renders as an empty heading. */}
        {(who?.role === RoleAdmin || who?.role === RoleSuperadmin) && (
          <div>
            <div className={groupHeaderClass}>Organization</div>
            <div className="space-y-0.5">
              <NavLink to="/accounts" className={linkClass} onClick={onNavigate}>
                <AccountsIcon />
                Accounts
              </NavLink>
              {who?.role === RoleSuperadmin && actAs === null && (
                <NavLink to="/environments" className={linkClass} onClick={onNavigate}>
                  <EnvironmentsIcon />
                  Environments
                </NavLink>
              )}
              {who?.role === RoleSuperadmin && actAs === null && (
                <NavLink to="/settings" className={linkClass} onClick={onNavigate}>
                  <SettingsIcon />
                  Settings
                </NavLink>
              )}
            </div>
          </div>
        )}
      </nav>
    </div>
  )
}

// Small avatar-triggered popover carrying the identity actions that used to
// sit pinned to the sidebar's own footer (username/role/sign out) — moved
// into the header, the same place a Pangolin-style dashboard puts its own
// account menu, so the sidebar stays pure navigation.
function UserMenu({ who }: { who: { username: string; role: string; namespace: string } }) {
  const [open, setOpen] = useState(false)

  useEffect(() => {
    if (!open) return
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') setOpen(false)
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [open])

  return (
    <div className="relative">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        aria-expanded={open}
        aria-label="Account menu"
        className="flex items-center gap-2 rounded-full py-1 pr-2 pl-1 transition-colors hover:bg-neutral-100 dark:hover:bg-neutral-800"
      >
        <span className="flex h-8 w-8 items-center justify-center rounded-full bg-neutral-900 text-xs font-semibold text-white dark:bg-white dark:text-neutral-900">
          {who.username.slice(0, 1).toUpperCase()}
        </span>
        <ChevronDownIcon width={14} height={14} className="text-neutral-400" />
      </button>

      {open && (
        <>
          <button type="button" aria-label="Close menu" className="fixed inset-0 z-40" onClick={() => setOpen(false)} />
          <div className="absolute right-0 z-50 mt-2 w-56 rounded-xl border border-neutral-200 bg-white p-3 shadow-lg ring-1 ring-black/5 dark:border-neutral-700 dark:bg-neutral-800 dark:ring-white/10">
            <div className="flex items-center justify-between">
              <span className="truncate text-sm font-medium text-neutral-900 dark:text-neutral-100">{who.username}</span>
              <span className="shrink-0 rounded-full bg-neutral-100 px-2 py-0.5 text-xs font-medium text-neutral-600 dark:bg-neutral-700 dark:text-neutral-300">
                {who.role}
              </span>
            </div>
            {/* A superadmin's own namespace is control-plane bookkeeping
                (see RoleSuperadmin's doc comment), not a tenant they'd
                recognize as "their org" — label it distinctly rather than
                implying they're scoped to one tenant among many. */}
            <span className="mt-0.5 block truncate text-xs text-neutral-500 dark:text-neutral-500">
              {who.role === RoleSuperadmin ? 'Control plane' : who.namespace}
            </span>
            <button
              type="button"
              onClick={() => logout()}
              className="mt-3 w-full rounded-lg px-2.5 py-1.5 text-left text-sm font-medium text-neutral-500 transition-colors hover:bg-neutral-100 hover:text-neutral-900 dark:text-neutral-500 dark:hover:bg-neutral-700/70 dark:hover:text-neutral-100"
            >
              Sign out
            </button>
          </div>
        </>
      )}
    </div>
  )
}

// Full-width top bar shared by mobile and desktop — logo on the left
// (joined by the drawer-open button on mobile only), theme toggle + account
// menu on the right. Replaces the old mobile-only top bar: previously the
// persistent sidebar carried the logo/theme/sign-out on desktop and a
// separate slim bar duplicated just the logo+menu-button on mobile: one
// header now covers both, matching the single full-width bar a
// Pangolin-style dashboard keeps above its own sidebar+content split.
function Header({ onOpenMobileMenu }: { onOpenMobileMenu: () => void }) {
  const who = useWhoami().data

  return (
    <header className="sticky top-0 z-30 flex h-14 shrink-0 items-center justify-between border-b border-neutral-200 bg-white px-4 sm:px-6 dark:border-neutral-800 dark:bg-neutral-900">
      <div className="flex items-center gap-3">
        <button
          type="button"
          onClick={onOpenMobileMenu}
          aria-label="Open menu"
          className="-ml-1.5 rounded-lg p-1.5 text-neutral-600 hover:bg-neutral-100 md:hidden dark:text-neutral-400 dark:hover:bg-neutral-800"
        >
          <MenuIcon width={22} height={22} />
        </button>
        <Logo className="h-5" />
      </div>
      <div className="flex items-center gap-4">
        <ThemeToggle />
        {who && (
          <>
            <div className="h-6 w-px bg-neutral-200 dark:bg-neutral-800" />
            <UserMenu who={who} />
          </>
        )}
      </div>
    </header>
  )
}

export function AppShell() {
  useSession() // re-renders this component on login/logout
  const [mobileOpen, setMobileOpen] = useState(false)

  return (
    <div className="flex min-h-screen flex-col bg-neutral-50 dark:bg-neutral-950">
      <Header onOpenMobileMenu={() => setMobileOpen(true)} />

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

      <div className="flex min-h-0 flex-1">
        <aside className="sticky top-14 hidden h-[calc(100vh-3.5rem)] w-60 shrink-0 border-r border-neutral-200 bg-white md:flex dark:border-neutral-800 dark:bg-neutral-900">
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
