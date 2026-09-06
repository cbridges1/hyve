import { useState, type FormEvent } from 'react'
import { login } from '../lib/api/auth'
import { ApiError } from '../lib/api/client'
import { Logo } from './Logo'
import { ThemeToggle } from './ThemeToggle'

const inputClass =
  'w-full rounded-lg border border-neutral-300 bg-white px-3 py-2 text-sm text-neutral-900 outline-none transition-colors placeholder:text-neutral-400 focus:border-neutral-500 focus:ring-2 focus:ring-neutral-900/10 dark:border-neutral-700 dark:bg-neutral-800 dark:text-neutral-100 dark:focus:border-neutral-500 dark:focus:ring-white/10'

export function LoginForm() {
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [org, setOrg] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [submitting, setSubmitting] = useState(false)

  async function onSubmit(e: FormEvent) {
    e.preventDefault()
    setError(null)
    setSubmitting(true)
    try {
      await login(username, password, org)
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
      <form
        onSubmit={onSubmit}
        className="w-full max-w-sm rounded-2xl border border-neutral-200 bg-white p-7 shadow-sm dark:border-neutral-800 dark:bg-neutral-900"
      >
        <div className="mb-7 flex justify-center">
          <Logo className="h-6" />
        </div>

        <label className="mb-3.5 block text-sm">
          <span className="mb-1.5 block font-medium text-neutral-600 dark:text-neutral-400">Username</span>
          <input
            type="text"
            value={username}
            onChange={(e) => setUsername(e.target.value)}
            required
            autoFocus
            autoComplete="username"
            className={inputClass}
          />
        </label>

        <label className="mb-3.5 block text-sm">
          <span className="mb-1.5 block font-medium text-neutral-600 dark:text-neutral-400">Password</span>
          <input
            type="password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            required
            autoComplete="current-password"
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
            placeholder="leave blank for a superadmin login"
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
          {submitting ? 'Signing in…' : 'Sign in'}
        </button>

        <p className="mt-5 text-center text-xs text-neutral-500 dark:text-neutral-500">
          Accounts are provisioned by an admin. There's no self-registration.
        </p>
      </form>
    </div>
  )
}
