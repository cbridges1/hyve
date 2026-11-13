// The console's building blocks: cards, label/value rows, pills, banners,
// the two-click delete, page headers.
import { useEffect, useRef, useState, type ReactNode } from 'react'
import { Link, useLocation } from 'react-router-dom'
import type { Status, Tone } from '../lib/format'
import { backOf } from '../lib/routes'
import { Icon } from './Icon'

export function Card({ title, action, children, className = '' }: { title?: ReactNode; action?: ReactNode; children: ReactNode; className?: string }) {
  return (
    <div className={`rounded-xl border border-neutral-200 bg-white p-4 shadow-sm sm:p-5 dark:border-neutral-800 dark:bg-neutral-900 ${className}`}>
      {(title || action) && (
        <div className="mb-3 flex items-center justify-between gap-3">
          {title && <h2 className="mb-0">{title}</h2>}
          {action}
        </div>
      )}
      {children}
    </div>
  )
}

// A back-to-list link, the same on every detail page.
// Back to the page that opened this one, if it said (lib/routes.ts
// backFrom), else to the given parent.
export function BackLink({ to, label }: { to: string; label: string }) {
  const back = backOf(useLocation())
  return (
    <Link
      to={back?.to || to}
      className="mb-3 inline-flex items-center gap-1 text-sm font-medium text-neutral-500 no-underline transition-colors hover:text-neutral-900 hover:no-underline dark:text-neutral-400 dark:hover:text-neutral-100"
    >
      <span className="rotate-180">
        <Icon name="chevronRight" size={14} />
      </span>
      {back?.label || label}
    </Link>
  )
}

// One label/value row of a detail page.
export function Field({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="flex flex-col gap-0.5 border-t border-neutral-100 py-2.5 first:border-t-0 sm:flex-row sm:items-baseline sm:gap-4 dark:border-neutral-800/70">
      <span className="w-full shrink-0 text-xs font-medium tracking-wide text-neutral-500 uppercase sm:w-40">{label}</span>
      <span className="min-w-0 text-sm text-neutral-800 dark:text-neutral-200">{children}</span>
    </div>
  )
}

export function CodeBlock({ children, className = 'max-h-96' }: { children: string; className?: string }) {
  return (
    <pre className={`${className} overflow-auto rounded-lg bg-neutral-50 p-3 font-mono text-xs whitespace-pre text-neutral-700 dark:bg-neutral-950 dark:text-neutral-300`}>
      {children}
    </pre>
  )
}

export function EmptyState({ children }: { children: ReactNode }) {
  return <p className="py-6 text-center text-sm text-neutral-500">{children}</p>
}

const TONES: Record<Tone, string> = {
  good: 'bg-green-100 text-green-800 dark:bg-green-950 dark:text-green-300',
  busy: 'bg-amber-100 text-amber-800 dark:bg-amber-950 dark:text-amber-300',
  bad: 'bg-red-100 text-red-800 dark:bg-red-950 dark:text-red-300',
  idle: 'bg-neutral-100 text-neutral-600 dark:bg-neutral-800 dark:text-neutral-400',
  env: 'bg-sky-100 text-sky-800 dark:bg-sky-950 dark:text-sky-300',
  info: 'bg-indigo-100 text-indigo-800 dark:bg-indigo-950 dark:text-indigo-300',
}

// The console's status pill.
export function Pill({ tone = 'idle', label, title }: { tone?: Tone; label: string; title?: string }) {
  return <span title={title || undefined} className={`inline-block rounded px-2 py-0.5 text-xs font-medium whitespace-nowrap ${TONES[tone]}`}>{label}</span>
}

export function StatusPill({ status }: { status: Status | null }) {
  return status ? <Pill tone={status.tone} label={status.label} title={status.title} /> : null
}

export function Banner({ tone = 'info', children }: { tone?: 'info' | 'success' | 'warning' | 'error'; children: ReactNode }) {
  return <div className={`banner banner-${tone}`}>{children}</div>
}

// An error, if there is one.
export function ErrorBanner({ error }: { error?: unknown }) {
  if (!error) return null
  const msg = error instanceof Error ? error.message : String(error)
  return msg ? <Banner tone="error">{msg}</Banner> : null
}

export function Loading({ label = 'Loading…' }: { label?: string }) {
  return (
    <div className="flex items-center gap-2 py-8 text-sm text-neutral-500">
      <span className="h-4 w-4 animate-spin rounded-full border-2 border-neutral-300 border-t-neutral-700 dark:border-neutral-700 dark:border-t-neutral-300" />
      {label}
    </div>
  )
}

// The console's red Delete: click, then click again to confirm.
export function ConfirmButton({
  label,
  confirm,
  onConfirm,
  disabled,
  variant = 'link',
}: {
  label: string
  confirm: string
  onConfirm: () => void
  disabled?: boolean
  // link: red text, for rows; danger: a full-size outlined red button.
  variant?: 'link' | 'danger'
}) {
  const [armed, setArmed] = useState(false)
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null)
  useEffect(() => () => {
    if (timer.current) clearTimeout(timer.current)
  }, [])
  return (
    <button
      type="button"
      disabled={disabled}
      className={
        variant === 'danger'
          ? `btn shrink-0 ${armed ? 'btn-danger' : 'border-red-300 text-red-600 hover:bg-red-50 dark:border-red-800 dark:text-red-400 dark:hover:bg-red-950/40'}`
          : `shrink-0 rounded-lg px-2.5 py-1 text-sm font-medium transition-colors disabled:opacity-50 ${
              armed ? 'bg-red-600 text-white hover:bg-red-700' : 'text-red-600 hover:bg-red-50 dark:text-red-400 dark:hover:bg-red-950/40'
            }`
      }
      onClick={(e) => {
        e.stopPropagation()
        if (armed) {
          setArmed(false)
          onConfirm()
          return
        }
        setArmed(true)
        timer.current = setTimeout(() => setArmed(false), 4000)
      }}
    >
      {armed ? confirm : label}
    </button>
  )
}

// A page's title row, with actions on the right.
export function PageHeader({ title, children, subtitle }: { title: ReactNode; children?: ReactNode; subtitle?: ReactNode }) {
  return (
    <div className="mb-4 flex flex-wrap items-center justify-between gap-3">
      <div className="min-w-0">
        <h1 className="mb-0">{title}</h1>
        {subtitle && <p className="mt-1 text-sm text-neutral-500">{subtitle}</p>}
      </div>
      {children && <div className="flex flex-wrap items-center gap-2.5">{children}</div>}
    </div>
  )
}

// A detail page's top (its back link, title, actions and tabs), kept under
// the app's header as the page scrolls; opaque, so what scrolls under it
// doesn't show through. Its bottom edge is --sticky-header, for sticky
// panes further down (the explorer's type list) to sit under it. The
// negative top margin takes in the main area's top padding, so it looks
// the same before it sticks. The gap below it is its own padding (mb-0
// overrides the page's space-y, which would add to it), so what follows
// never starts under it. On a phone it scrolls away with the page: kept, it
// would take much of the screen.
export function StickyHeader({ children }: { children: ReactNode }) {
  const box = useRef<HTMLDivElement>(null)
  useEffect(() => {
    const el = box.current
    if (!el) return
    const root = document.documentElement
    // --app-header: the app's header (AppShell), which this sits under.
    const update = () =>
      root.style.setProperty('--sticky-header', getComputedStyle(el).position === 'sticky' ? `calc(var(--app-header) + ${el.offsetHeight}px)` : 'var(--app-header)')
    update()
    const ro = new ResizeObserver(update)
    ro.observe(el)
    return () => {
      ro.disconnect()
      root.style.removeProperty('--sticky-header')
    }
  }, [])
  return (
    <div
      ref={box}
      className="z-20 -mt-5 md:sticky md:top-[var(--app-header)] mb-0 space-y-4 bg-neutral-50 pt-5 pb-4 sm:-mt-6 sm:pt-6 dark:bg-neutral-950"
    >
      {children}
    </div>
  )
}

// Tabs that are links (a detail page's sections).
export function Tabs({ tabs, className = 'mb-4' }: { tabs: { label: string; to: string; active: boolean }[]; className?: string }) {
  return (
    // A phone scrolls them sideways rather than wrapping; the rule under
    // them is a shadow, which the scrolling box doesn't clip as it would
    // the active tab's border over a border of its own.
    <div className={`flex gap-1 overflow-x-auto shadow-[inset_0_-1px_0] shadow-neutral-200 [scrollbar-width:none] dark:shadow-neutral-800 ${className}`}>
      {tabs.map((t) => (
        <Link
          key={t.to}
          to={t.to}
          className={`shrink-0 border-b-2 px-3 py-2.5 text-sm font-medium whitespace-nowrap no-underline hover:no-underline ${
            t.active
              ? 'border-neutral-900 text-neutral-900 dark:border-white dark:text-white'
              : 'border-transparent text-neutral-500 hover:text-neutral-800 dark:hover:text-neutral-200'
          }`}
        >
          {t.label}
        </Link>
      ))}
    </div>
  )
}
