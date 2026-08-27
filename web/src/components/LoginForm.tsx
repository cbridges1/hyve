import { useState, type FormEvent } from 'react'
import { login } from '../lib/api/auth'
import { ApiError } from '../lib/api/client'

export function LoginForm() {
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [submitting, setSubmitting] = useState(false)

  async function onSubmit(e: FormEvent) {
    e.preventDefault()
    setError(null)
    setSubmitting(true)
    try {
      await login(username, password)
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Failed to reach server')
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <div className="flex min-h-screen items-center justify-center bg-neutral-50 dark:bg-neutral-950">
      <form
        onSubmit={onSubmit}
        className="w-full max-w-sm rounded-lg border border-neutral-200 bg-white p-6 shadow-sm dark:border-neutral-800 dark:bg-neutral-900"
      >
        <h1 className="mb-6 text-lg font-semibold text-neutral-900 dark:text-neutral-100">
          Sign in to hyve
        </h1>

        <label className="mb-3 block text-sm">
          <span className="mb-1 block text-neutral-600 dark:text-neutral-400">Username</span>
          <input
            type="text"
            value={username}
            onChange={(e) => setUsername(e.target.value)}
            required
            autoFocus
            autoComplete="username"
            className="w-full rounded border border-neutral-300 px-3 py-2 text-sm dark:border-neutral-700 dark:bg-neutral-800 dark:text-neutral-100"
          />
        </label>

        <label className="mb-4 block text-sm">
          <span className="mb-1 block text-neutral-600 dark:text-neutral-400">Password</span>
          <input
            type="password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            required
            autoComplete="current-password"
            className="w-full rounded border border-neutral-300 px-3 py-2 text-sm dark:border-neutral-700 dark:bg-neutral-800 dark:text-neutral-100"
          />
        </label>

        {error && (
          <p className="mb-4 rounded bg-red-50 px-3 py-2 text-sm text-red-700 dark:bg-red-950 dark:text-red-300">
            {error}
          </p>
        )}

        <button
          type="submit"
          disabled={submitting}
          className="w-full rounded bg-neutral-900 px-3 py-2 text-sm font-medium text-white disabled:opacity-50 dark:bg-neutral-100 dark:text-neutral-900"
        >
          {submitting ? 'Signing in…' : 'Sign in'}
        </button>

        <p className="mt-4 text-xs text-neutral-500 dark:text-neutral-500">
          Accounts are provisioned via{' '}
          <code className="rounded bg-neutral-100 px-1 dark:bg-neutral-800">
            hyve cluster-config api create-user
          </code>
          . There's no self-registration.
        </p>
      </form>
    </div>
  )
}
