import { useEffect, type ReactNode } from 'react'
import { createPortal } from 'react-dom'

// The console's overlay dialog, for "New X" forms and anything else that
// pops up over a page. A persistent one closes only from its own buttons,
// not Escape or a click outside: for forms that would lose much typing.
// The page behind doesn't scroll while one is open; an 'xl' one fits the
// window and scrolls inside, so an editor in it is seen whole.
export function Modal({
  title,
  onClose,
  children,
  wide = false,
  persistent = false,
}: {
  title: string
  onClose: () => void
  children: ReactNode
  // true: room for a table or an editor; 'xl': most of the window.
  wide?: boolean | 'xl'
  persistent?: boolean
}) {
  useEffect(() => {
    if (persistent) return
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose()
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [onClose, persistent])

  // The page stays put under the modal; a wheel past the end of the
  // modal's content (or an editor's) mustn't scroll it.
  useEffect(() => {
    const root = document.documentElement
    const was = root.style.overflow
    root.style.overflow = 'hidden'
    return () => {
      root.style.overflow = was
    }
  }, [])

  return createPortal(
    <div className={`fixed inset-0 z-50 flex items-start justify-center overflow-y-auto overscroll-contain p-4 ${wide === 'xl' ? 'pt-[4vh]' : 'pt-[10vh]'}`}>
      <button
        type="button"
        aria-label="Close"
        tabIndex={persistent ? -1 : undefined}
        className="fixed inset-0 bg-black/50 backdrop-blur-sm"
        onClick={persistent ? undefined : onClose}
      />
      <div
        className={`relative w-full ${wide === 'xl' ? 'flex max-h-[92vh] max-w-6xl flex-col' : wide ? 'max-w-3xl' : 'max-w-lg'} rounded-2xl border border-neutral-200 bg-white p-5 shadow-2xl ring-1 ring-black/5 dark:border-neutral-700 dark:bg-neutral-800 dark:ring-white/10`}
      >
        <div className="mb-4 flex shrink-0 items-center justify-between">
          <h2 className="mb-0 text-base font-semibold text-neutral-900 dark:text-neutral-100">{title}</h2>
          <button
            type="button"
            onClick={onClose}
            aria-label="Close"
            className="rounded-lg p-1 text-lg leading-none text-neutral-400 transition-colors hover:bg-neutral-100 hover:text-neutral-600 dark:hover:bg-neutral-700 dark:hover:text-neutral-300"
          >
            ✕
          </button>
        </div>
        {wide === 'xl' ? <div className="-mx-5 min-h-0 overflow-y-auto overscroll-contain px-5">{children}</div> : children}
      </div>
    </div>,
    document.body,
  )
}
