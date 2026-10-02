import type { ReactNode } from 'react'
import { Link } from 'react-router-dom'
import { ChevronRightIcon } from './icons'

export function Card({ title, action, children }: { title?: string; action?: ReactNode; children: ReactNode }) {
  return (
    <div className="rounded-xl border border-neutral-200 bg-white p-4 shadow-sm sm:p-5 dark:border-neutral-800 dark:bg-neutral-900">
      {(title || action) && (
        <div className="mb-3 flex items-center justify-between">
          {title && <h2 className="text-sm font-semibold text-neutral-900 dark:text-neutral-100">{title}</h2>}
          {action}
        </div>
      )}
      {children}
    </div>
  )
}

/** A back-to-list link, consistent across every detail page. */
export function BackLink({ to, label }: { to: string; label: string }) {
  return (
    <Link
      to={to}
      className="mb-3 inline-flex items-center gap-1 text-sm font-medium text-neutral-500 transition-colors hover:text-neutral-900 dark:text-neutral-400 dark:hover:text-neutral-100"
    >
      <span className="rotate-180"><ChevronRightIcon width={14} height={14} /></span>
      {label}
    </Link>
  )
}

/** A single label/value row — the workhorse of every detail page's "here's every field" view. */
export function Field({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="flex flex-col gap-0.5 border-t border-neutral-100 py-2.5 first:border-t-0 sm:flex-row sm:items-baseline sm:gap-4 dark:border-neutral-800/70">
      <span className="w-full shrink-0 text-xs font-medium tracking-wide text-neutral-500 uppercase sm:w-40 dark:text-neutral-500">
        {label}
      </span>
      <span className="min-w-0 text-sm text-neutral-800 dark:text-neutral-200">{children}</span>
    </div>
  )
}

export function CodeBlock({ children }: { children: string }) {
  return (
    <pre className="max-h-96 overflow-auto rounded-lg bg-neutral-50 p-3 font-mono text-xs text-neutral-700 dark:bg-neutral-950 dark:text-neutral-300">
      {children}
    </pre>
  )
}

export function EmptyState({ children }: { children: ReactNode }) {
  return <p className="py-6 text-center text-sm text-neutral-500 dark:text-neutral-500">{children}</p>
}
