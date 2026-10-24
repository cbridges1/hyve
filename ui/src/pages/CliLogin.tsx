// The browser half of `hyve login`: sign in here as anywhere in the UI,
// confirm, and the page hands the CLI a session of its own, encrypted to
// the key the CLI made, at the CLI's localhost callback
// (internal/login/browser.go has the contract).
import { useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import { request, errorMessage } from '../lib/api'
import { startSession } from '../lib/start'
import { useSession } from '../lib/session'
import { Divider, PasswordForm, SignInFrame, SSOButton, useAuthMethods } from './Login'

function fromB64url(s: string): Uint8Array<ArrayBuffer> {
  const b = atob(s.replace(/-/g, '+').replace(/_/g, '/'))
  const out = new Uint8Array(b.length)
  for (let i = 0; i < b.length; i++) out[i] = b.charCodeAt(i)
  return out
}

function toB64url(buf: ArrayBuffer): string {
  let s = ''
  for (const c of new Uint8Array(buf)) s += String.fromCharCode(c)
  return btoa(s).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '')
}

export default function CliLogin() {
  const [query] = useSearchParams()
  const { user } = useSession()
  const methods = useAuthMethods()
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const port = query.get('port') || ''
  const state = query.get('state') || ''
  const publicKey = query.get('publicKey') || ''
  const valid = /^\d{1,5}$/.test(port) && Number(port) > 0 && Number(port) < 65536 && !!state && !!publicKey
  const callback = (q: Record<string, string>) => `http://127.0.0.1:${port}/callback?${new URLSearchParams({ state, ...q })}`

  async function allow() {
    setBusy(true)
    setError('')
    try {
      if (!window.crypto?.subtle) throw new Error('This page needs HTTPS (or localhost) to hand the CLI its session.')
      const key = await crypto.subtle.importKey('spki', fromB64url(publicKey), { name: 'RSA-OAEP', hash: 'SHA-256' }, false, ['encrypt'])
      const s = await request<{ token: string; expiresAt: string }>('POST', '/v1/cli-sessions', {})
      const sealed = await crypto.subtle.encrypt({ name: 'RSA-OAEP' }, key, new TextEncoder().encode(s.token))
      window.location.href = callback({ token: toB64url(sealed), expiresAt: s.expiresAt || '' })
    } catch (e) {
      setError(errorMessage(e))
      setBusy(false)
    }
  }

  if (!valid) {
    return (
      <SignInFrame>
        <p className="banner banner-error">This link is incomplete. Run <code>hyve login</code> again.</p>
      </SignInFrame>
    )
  }

  if (!user) {
    const here = window.location.pathname + window.location.search
    return (
      <SignInFrame>
        <p className="mb-5 text-center text-sm text-neutral-600 dark:text-neutral-400">Sign in to connect the hyve CLI.</p>
        {query.get('error') && <p className="banner banner-error mb-5">{query.get('error')}</p>}
        {methods && (
          <>
            <SSOButton methods={methods} redirect={here} />
            {methods.oidc && methods.passwords && <Divider />}
            {methods.passwords && <PasswordForm onSignedIn={startSession} />}
          </>
        )}
      </SignInFrame>
    )
  }

  return (
    <SignInFrame>
      <h1 className="text-center">Sign in the hyve CLI?</h1>
      <p className="mb-5 text-center text-sm text-neutral-600 dark:text-neutral-400">
        The <code>hyve</code> command on this computer will act as <strong>{user.username}</strong> until its session ends. Only allow this if you just
        ran <code>hyve login</code>.
      </p>
      {error && <p className="banner banner-error mb-4">{error}</p>}
      <div className="flex gap-2">
        <button type="button" className="btn btn-secondary flex-1" disabled={busy} onClick={() => (window.location.href = callback({ error: 'cancelled' }))}>
          Cancel
        </button>
        <button type="button" className="btn btn-primary flex-1" disabled={busy} onClick={allow}>
          {busy ? 'Signing in…' : 'Allow'}
        </button>
      </div>
    </SignInFrame>
  )
}
