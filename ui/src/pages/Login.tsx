// Sign-in: a username and password against the host's Users. A User that
// must change its password does it here before signing in.
import { useEffect, useState, type FormEvent } from 'react'
import { useNavigate, useSearchParams } from 'react-router-dom'
import { Logo } from '../components/Logo'
import { ThemeToggle } from '../components/ThemeToggle'
import { ApiError, auth, authMethods, type AuthMethods } from '../lib/api'
import { startSession } from '../lib/start'

export function SignInFrame({ children, onSubmit }: { children: React.ReactNode; onSubmit?: (e: FormEvent) => void }) {
  return (
    <div className="relative flex min-h-screen items-center justify-center bg-neutral-50 px-4 dark:bg-neutral-950">
      <div className="absolute top-4 right-4">
        <ThemeToggle />
      </div>
      <form
        className="w-full max-w-sm rounded-2xl border border-neutral-200 bg-white p-7 shadow-sm dark:border-neutral-800 dark:bg-neutral-900"
        onSubmit={(e) => {
          e.preventDefault()
          onSubmit?.(e)
        }}
      >
        <div className="mb-7 flex justify-center">
          <Logo className="h-6" />
        </div>
        {children}
      </form>
    </div>
  )
}

// The username/password fields and the forced password change, shared by
// the UI's sign-in and the CLI's (CliLogin). onSignedIn runs once the host
// has set the session cookie.
export function PasswordForm({ onSignedIn, submitLabel = 'Sign in' }: { onSignedIn: () => Promise<void>; submitLabel?: string }) {
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [newPassword, setNewPassword] = useState('')
  const [again, setAgain] = useState('')
  const [mustChange, setMustChange] = useState(false)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  async function submit() {
    setError('')
    if (mustChange && newPassword !== again) {
      setError("The new passwords don't match")
      return
    }
    setBusy(true)
    try {
      await auth.login(username, password, mustChange ? newPassword : '')
      await onSignedIn()
    } catch (e) {
      if (e instanceof ApiError && (e.body as any)?.code === 'MustChangePassword') setMustChange(true)
      else setError(e instanceof ApiError && e.status === 401 ? 'Invalid username or password' : (e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  const field = (label: string, value: string, set: (v: string) => void, type: string, autoComplete: string, disabled = false, autoFocus = false) => (
    <label className="mb-3.5 block text-sm">
      <span className="mb-1.5 block font-medium text-neutral-600 dark:text-neutral-400">{label}</span>
      <input
        type={type}
        required
        value={value}
        autoFocus={autoFocus}
        autoComplete={autoComplete}
        disabled={disabled}
        onChange={(e) => set(e.target.value)}
      />
    </label>
  )

  return (
    <div
      onKeyDown={(e) => {
        if (e.key === 'Enter') {
          e.preventDefault()
          if (!busy) submit()
        }
      }}
    >
      {field('Username', username, setUsername, 'text', 'username', mustChange, true)}
      {field('Password', password, setPassword, 'password', 'current-password', mustChange)}
      {mustChange && (
        <>
          <p className="banner banner-info">Choose a new password to finish signing in.</p>
          {field('New password', newPassword, setNewPassword, 'password', 'new-password')}
          {field('New password again', again, setAgain, 'password', 'new-password')}
        </>
      )}
      {error && <p className="banner banner-error mb-5">{error}</p>}
      <button
        type="button"
        disabled={busy || !username || !password}
        onClick={submit}
        className="w-full rounded-lg bg-neutral-900 px-3 py-2.5 text-sm font-medium text-white transition-colors hover:bg-neutral-800 disabled:opacity-50 dark:bg-white dark:text-neutral-900 dark:hover:bg-neutral-200"
      >
        {busy ? 'Signing in…' : submitLabel}
      </button>
    </div>
  )
}

// How this host signs people in; null until known.
export function useAuthMethods(): AuthMethods | null {
  const [m, setM] = useState<AuthMethods | null>(null)
  useEffect(() => {
    authMethods().then(setM)
  }, [])
  return m
}

// The identity provider's button: the host takes the browser there and
// back to redirect.
export function SSOButton({ methods, redirect }: { methods: AuthMethods; redirect: string }) {
  if (!methods.oidc) return null
  return (
    <a
      href={`${methods.oidc.startUrl}?redirect=${encodeURIComponent(redirect)}`}
      className="flex w-full items-center justify-center rounded-lg border border-neutral-300 bg-white px-3 py-2.5 text-sm font-medium text-neutral-800 no-underline transition-colors hover:bg-neutral-50 hover:no-underline dark:border-neutral-700 dark:bg-neutral-800 dark:text-neutral-100 dark:hover:bg-neutral-700"
    >
      Sign in with {methods.oidc.displayName}
    </a>
  )
}

export function Divider() {
  return (
    <div className="my-5 flex items-center gap-3 text-xs text-neutral-400">
      <span className="h-px flex-1 bg-neutral-200 dark:bg-neutral-800" />
      or
      <span className="h-px flex-1 bg-neutral-200 dark:bg-neutral-800" />
    </div>
  )
}

export default function Login() {
  const navigate = useNavigate()
  const [query] = useSearchParams()
  const methods = useAuthMethods()
  const redirect = query.get('redirect') || '/'
  const error = query.get('error')
  return (
    <SignInFrame>
      {error && <p className="banner banner-error mb-5">{error}</p>}
      {methods && (
        <>
          <SSOButton methods={methods} redirect={redirect} />
          {methods.oidc && methods.passwords && <Divider />}
          {methods.passwords && (
            <PasswordForm
              onSignedIn={async () => {
                await startSession()
                navigate(redirect, { replace: true })
              }}
            />
          )}
        </>
      )}
      <p className="mt-5 text-center text-xs text-neutral-500">
        Accounts are made by an admin. From the CLI, <code>hyve ui</code> opens this signed in.
      </p>
    </SignInFrame>
  )
}
