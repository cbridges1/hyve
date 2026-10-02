import { createContext, useCallback, useContext, useState, type ReactNode } from 'react'

type ConfirmOptions = {
  title: string
  message: string
  confirmLabel?: string
  cancelLabel?: string
  /** Renders the confirm button in red — use for anything that destroys data (delete cluster/template/account/...). */
  danger?: boolean
}

type PendingConfirm = ConfirmOptions & { resolve: (ok: boolean) => void }

const ConfirmContext = createContext<((opts: ConfirmOptions) => Promise<boolean>) | null>(null)

/**
 * Renders nothing by default; when any component calls the confirm()
 * function this provides, it shows a modal and resolves the returned
 * Promise once the user picks an option. One shared instance replaces
 * every bare `window.confirm(...)` call across the app — window.confirm
 * can't be styled, doesn't match the rest of the UI, and (per the user's
 * own explicit ask) critical/destructive actions deserve a real
 * confirmation dialog, not a browser-native one.
 */
export function ConfirmProvider({ children }: { children: ReactNode }) {
  const [pending, setPending] = useState<PendingConfirm | null>(null)

  const confirm = useCallback((opts: ConfirmOptions) => {
    return new Promise<boolean>((resolve) => {
      setPending({ ...opts, resolve })
    })
  }, [])

  function settle(ok: boolean) {
    pending?.resolve(ok)
    setPending(null)
  }

  return (
    <ConfirmContext.Provider value={confirm}>
      {children}
      {pending && (
        <div className="fixed inset-0 z-50 flex items-center justify-center p-4">
          <button
            type="button"
            aria-label="Cancel"
            className="absolute inset-0 bg-black/50 backdrop-blur-sm"
            onClick={() => settle(false)}
          />
          <div className="relative w-full max-w-sm rounded-2xl border border-neutral-200 bg-white p-5 shadow-2xl ring-1 ring-black/5 dark:border-neutral-700 dark:bg-neutral-800 dark:ring-white/10">
            <h2 className="text-base font-semibold text-neutral-900 dark:text-neutral-100">{pending.title}</h2>
            <p className="mt-2 text-sm text-neutral-600 dark:text-neutral-400">{pending.message}</p>
            <div className="mt-5 flex justify-end gap-2">
              <button
                type="button"
                onClick={() => settle(false)}
                className="rounded-lg px-3 py-1.5 text-sm font-medium text-neutral-600 transition-colors hover:bg-neutral-100 dark:text-neutral-400 dark:hover:bg-neutral-700"
              >
                {pending.cancelLabel ?? 'Cancel'}
              </button>
              <button
                type="button"
                autoFocus
                onClick={() => settle(true)}
                className={`rounded-lg px-3 py-1.5 text-sm font-medium text-white transition-colors ${
                  pending.danger
                    ? 'bg-red-600 hover:bg-red-700 dark:bg-red-600 dark:hover:bg-red-500'
                    : 'bg-neutral-900 hover:bg-neutral-800 dark:bg-white dark:text-neutral-900 dark:hover:bg-neutral-200'
                }`}
              >
                {pending.confirmLabel ?? 'Confirm'}
              </button>
            </div>
          </div>
        </div>
      )}
    </ConfirmContext.Provider>
  )
}

/** Returns an async confirm(opts) function — `if (!(await confirm({...}))) return` reads naturally at every former `if (!confirm(...)) return` call site. */
export function useConfirm() {
  const ctx = useContext(ConfirmContext)
  if (!ctx) throw new Error('useConfirm must be used within a ConfirmProvider')
  return ctx
}
