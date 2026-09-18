import { useState, type FormEvent } from 'react'
import { Link } from 'react-router-dom'
import { requestPasswordReset } from '../lib/api/auth'
import { ApiError } from '../lib/api/client'
import { Logo } from '../components/Logo'
import { ThemeToggle } from '../components/ThemeToggle'

const inputClass =
  'w-full rounded-lg border border-neutral-300 bg-white px-3 py-2 text-sm text-neutral-900 outline-none transition-colors placeholder:text-neutral-400 focus:border-neutral-500 focus:ring-2 focus:ring-neutral-900/10 dark:border-neutral-700 dark:bg-neutral-800 dark:text-neutral-100 dark:focus:border-neutral-500 dark:focus:ring-white/10'

// ForgotPasswordPage mirrors LoginForm's own layout — same card, same
// spacing — since a visitor lands here directly from the login screen's
// own link, not from anywhere deeper in the console.
export function ForgotPasswordPage() {
  const [identifier, setIdentifier] = useState('')
  const [org, setOrg] = useState('')
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [sent, setSent] = useState(false)

  async function onSubmit(e: FormEvent) {
    e.preventDefault()
    setError(null)
    setSubmitting(true)
    try {
      await requestPasswordReset(identifier, org)
      // Always show the same confirmation, whether or not identifier
      // actually matched an account — the backend already stays generic
      // for the same reason (see internal/api.handleRequestPasswordReset's
      // own doc comment); showing anything more specific here would just
      // undo that on the client.
      setSent(true)
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Failed to reach server')
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <div className="relative flex min-h-screen items-center justify-center bg-neutral-50 px-4 dark:bg-neutral-950">
      <div className="absolute top-4 right-4 w-32">
        <ThemeToggle />
      </div>
      <div className="w-full max-w-sm rounded-2xl border border-neutral-200 bg-white p-7 shadow-sm dark:border-neutral-800 dark:bg-neutral-900">
        <div className="mb-7 flex justify-center">
          <Logo className="h-6" />
        </div>

        {sent ? (
          <>
            <p className="mb-5 rounded-lg bg-neutral-50 px-3 py-2.5 text-sm text-neutral-700 dark:bg-neutral-800 dark:text-neutral-300">
              If that account exists and has an email on file, a password reset link is on its way. Check your inbox.
            </p>
            <Link
              to="/login"
              className="block w-full rounded-lg bg-neutral-900 px-3 py-2.5 text-center text-sm font-medium text-white transition-colors hover:bg-neutral-800 dark:bg-white dark:text-neutral-900 dark:hover:bg-neutral-200"
            >
              Back to sign in
            </Link>
          </>
        ) : (
          <form onSubmit={onSubmit}>
            <p className="mb-5 text-sm text-neutral-600 dark:text-neutral-400">
              Enter your username or email — if an account matches, we'll send a reset link.
            </p>

            <label className="mb-3.5 block text-sm">
              <span className="mb-1.5 block font-medium text-neutral-600 dark:text-neutral-400">Username or email</span>
              <input
                type="text"
                value={identifier}
                onChange={(e) => setIdentifier(e.target.value)}
                required
                autoFocus
                autoComplete="username"
                className={inputClass}
              />
            </label>

            <label className="mb-5 block text-sm">
              <span className="mb-1.5 block font-medium text-neutral-600 dark:text-neutral-400">
                Organization <span className="font-normal text-neutral-400 dark:text-neutral-500">(optional)</span>
              </span>
              <input
                type="text"
                value={org}
                onChange={(e) => setOrg(e.target.value)}
                placeholder="leave blank for a superadmin account"
                autoComplete="organization"
                className={inputClass}
              />
            </label>

            {error && (
              <p className="mb-5 rounded-lg bg-red-50 px-3 py-2 text-sm text-red-700 dark:bg-red-950/60 dark:text-red-300">
                {error}
              </p>
            )}

            <button
              type="submit"
              disabled={submitting}
              className="w-full rounded-lg bg-neutral-900 px-3 py-2.5 text-sm font-medium text-white transition-colors hover:bg-neutral-800 disabled:opacity-50 dark:bg-white dark:text-neutral-900 dark:hover:bg-neutral-200"
            >
              {submitting ? 'Sending…' : 'Send reset link'}
            </button>

            <Link
              to="/login"
              className="mt-4 block text-center text-xs font-medium text-neutral-500 hover:text-neutral-900 dark:text-neutral-500 dark:hover:text-neutral-100"
            >
              Back to sign in
            </Link>
          </form>
        )}
      </div>
    </div>
  )
}
