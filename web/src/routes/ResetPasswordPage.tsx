import { useState, type FormEvent } from 'react'
import { Link, useSearchParams } from 'react-router-dom'
import { resetPassword } from '../lib/api/auth'
import { ApiError } from '../lib/api/client'
import { Logo } from '../components/Logo'
import { ThemeToggle } from '../components/ThemeToggle'

const inputClass =
  'w-full rounded-lg border border-neutral-300 bg-white px-3 py-2 text-sm text-neutral-900 outline-none transition-colors placeholder:text-neutral-400 focus:border-neutral-500 focus:ring-2 focus:ring-neutral-900/10 dark:border-neutral-700 dark:bg-neutral-800 dark:text-neutral-100 dark:focus:border-neutral-500 dark:focus:ring-white/10'

// ResetPasswordPage is where a password-reset email's link lands — email/
// token/namespace all come from the query string exactly as
// internal/api.buildPasswordResetLink wrote them (see that function's own
// doc comment for why namespace has to round-trip unchanged rather than
// being re-derived from email here).
export function ResetPasswordPage() {
  const [params] = useSearchParams()
  const email = params.get('email') ?? ''
  const token = params.get('token') ?? ''
  const namespace = params.get('namespace') ?? undefined

  const [newPassword, setNewPassword] = useState('')
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [done, setDone] = useState(false)

  const missingParams = !email || !token

  async function onSubmit(e: FormEvent) {
    e.preventDefault()
    setError(null)
    setSubmitting(true)
    try {
      await resetPassword(email, token, newPassword, namespace)
      setDone(true)
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

        {missingParams ? (
          <>
            <p className="mb-5 rounded-lg bg-red-50 px-3 py-2 text-sm text-red-700 dark:bg-red-950/60 dark:text-red-300">
              This reset link is missing its email or code. Request a new one.
            </p>
            <Link
              to="/forgot-password"
              className="block w-full rounded-lg bg-neutral-900 px-3 py-2.5 text-center text-sm font-medium text-white transition-colors hover:bg-neutral-800 dark:bg-white dark:text-neutral-900 dark:hover:bg-neutral-200"
            >
              Request a new link
            </Link>
          </>
        ) : done ? (
          <>
            <p className="mb-5 rounded-lg bg-neutral-50 px-3 py-2.5 text-sm text-neutral-700 dark:bg-neutral-800 dark:text-neutral-300">
              Your password has been reset. You can now sign in with your new password.
            </p>
            <Link
              to="/login"
              className="block w-full rounded-lg bg-neutral-900 px-3 py-2.5 text-center text-sm font-medium text-white transition-colors hover:bg-neutral-800 dark:bg-white dark:text-neutral-900 dark:hover:bg-neutral-200"
            >
              Sign in
            </Link>
          </>
        ) : (
          <form onSubmit={onSubmit}>
            <p className="mb-5 text-sm text-neutral-600 dark:text-neutral-400">
              Resetting the password for <span className="font-medium text-neutral-800 dark:text-neutral-200">{email}</span>.
            </p>

            <label className="mb-5 block text-sm">
              <span className="mb-1.5 block font-medium text-neutral-600 dark:text-neutral-400">New password</span>
              <input
                type="password"
                value={newPassword}
                onChange={(e) => setNewPassword(e.target.value)}
                required
                autoFocus
                autoComplete="new-password"
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
              {submitting ? 'Resetting…' : 'Reset password'}
            </button>
          </form>
        )}
      </div>
    </div>
  )
}
