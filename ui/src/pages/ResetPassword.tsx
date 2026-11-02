// Forgot password: ask for a code by username or email address, then set a
// new password with it. The emailed link opens the second step with both
// filled in (/reset-password?user=&code=).
import { useState } from 'react'
import { Link, useNavigate, useSearchParams } from 'react-router-dom'
import { ApiError, auth } from '../lib/api'
import { SignInFrame } from './Login'

const primary =
  'w-full rounded-lg bg-neutral-900 px-3 py-2.5 text-sm font-medium text-white transition-colors hover:bg-neutral-800 disabled:opacity-50 dark:bg-white dark:text-neutral-900 dark:hover:bg-neutral-200'

function Field({
  label,
  value,
  onChange,
  type = 'text',
  autoComplete,
  autoFocus,
}: {
  label: string
  value: string
  onChange: (v: string) => void
  type?: string
  autoComplete?: string
  autoFocus?: boolean
}) {
  return (
    <label className="mb-3.5 block text-sm">
      <span className="mb-1.5 block font-medium text-neutral-600 dark:text-neutral-400">{label}</span>
      <input type={type} required value={value} autoFocus={autoFocus} autoComplete={autoComplete} spellCheck={false} onChange={(e) => onChange(e.target.value)} />
    </label>
  )
}

export default function ResetPassword() {
  const navigate = useNavigate()
  const [query] = useSearchParams()
  const [identifier, setIdentifier] = useState(query.get('user') || '')
  const [code, setCode] = useState(query.get('code') || '')
  const [step, setStep] = useState<'ask' | 'reset' | 'done'>(query.get('code') ? 'reset' : 'ask')
  const [password, setPassword] = useState('')
  const [again, setAgain] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  async function run(fn: () => Promise<void>) {
    setError('')
    setBusy(true)
    try {
      await fn()
    } catch (e) {
      setError(e instanceof ApiError && e.status === 429 ? 'Too many attempts. Try again later.' : (e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  const ask = () =>
    run(async () => {
      await auth.requestReset(identifier.trim())
      setStep('reset')
    })

  const reset = () =>
    run(async () => {
      if (password !== again) throw new Error("The new passwords don't match")
      await auth.confirmReset(identifier.trim(), code.trim(), password)
      setStep('done')
    })

  return (
    <SignInFrame onSubmit={() => !busy && (step === 'ask' ? ask() : step === 'reset' ? reset() : navigate('/login'))}>
      <h1 className="mb-1 text-center text-lg">Reset your password</h1>
      {step === 'ask' && (
        <>
          <p className="text-muted mb-5 text-center text-sm">We'll email a code to the address on your account.</p>
          <Field label="Username or email" value={identifier} onChange={setIdentifier} autoComplete="username" autoFocus />
          {error && <p className="banner banner-error mb-5">{error}</p>}
          <button type="submit" className={primary} disabled={busy || !identifier.trim()}>
            {busy ? 'Sending…' : 'Email me a code'}
          </button>
        </>
      )}
      {step === 'reset' && (
        <>
          <p className="text-muted mb-5 text-center text-sm">
            {query.get('code')
              ? 'Choose a new password.'
              : 'If that account has an email address, a code is on its way. It works for 2 hours. No email? Ask an admin to set your password.'}
          </p>
          {query.get('code') && !query.get('user') && <Field label="Username or email" value={identifier} onChange={setIdentifier} autoComplete="username" autoFocus />}
          {!query.get('code') && <Field label="Code" value={code} onChange={setCode} autoComplete="one-time-code" autoFocus />}
          <Field label="New password" type="password" value={password} onChange={setPassword} autoComplete="new-password" autoFocus={!!query.get('code')} />
          <Field label="New password again" type="password" value={again} onChange={setAgain} autoComplete="new-password" />
          <p className="hint -mt-2 mb-4">At least 12 characters. Resetting signs you out everywhere.</p>
          {error && <p className="banner banner-error mb-5">{error}</p>}
          <button type="submit" className={primary} disabled={busy || !identifier.trim() || !code.trim() || !password || !again}>
            {busy ? 'Resetting…' : 'Reset password'}
          </button>
        </>
      )}
      {step === 'done' && (
        <>
          <p className="banner banner-success mb-5">Your password is changed. Sign in with the new one.</p>
          <button type="submit" className={primary}>
            Sign in
          </button>
        </>
      )}
      <p className="mt-5 text-center text-xs">
        <Link to="/login">Back to sign-in</Link>
      </p>
    </SignInFrame>
  )
}
