// A user's API tokens (`hyve user token`): list, make and revoke. A new
// token is shown once, here; the host keeps only its hash. Your own come
// from the account menu, a CI user's from its row on Users (that takes
// the users/tokens right, which admins have).
import { useCallback, useEffect, useState } from 'react'
import { errorMessage, tokens, type TokenInfo } from '../lib/api'
import { useAction } from '../lib/hooks'
import { SelectField, TextField } from './form'
import { Modal } from './Modal'
import { ConfirmButton, EmptyState, ErrorBanner, Loading } from './ui'

const NAME_RE = /^[a-z0-9]([-a-z0-9]*[a-z0-9])?$/

const EXPIRIES = [
  { value: '7d', label: '7 days' },
  { value: '30d', label: '30 days' },
  { value: '90d', label: '90 days' },
  { value: '1y', label: '1 year (the most)' },
]

const day = (ts: string) => new Date(ts).toLocaleDateString()

export function ApiTokens({ user, own, onClose }: { user: string; own: boolean; onClose: () => void }) {
  const [list, setList] = useState<TokenInfo[] | null>(null)
  const [loadError, setLoadError] = useState('')
  const [name, setName] = useState('')
  const [description, setDescription] = useState('')
  const [expiresIn, setExpiresIn] = useState('90d')
  const [made, setMade] = useState<{ name: string; token: string } | null>(null)
  const [copied, setCopied] = useState(false)
  const { busy, error, setError, run } = useAction()

  const load = useCallback(() => {
    tokens.list(own ? undefined : user).then(setList, (e) => setLoadError(errorMessage(e)))
  }, [user, own])
  useEffect(load, [load])

  async function create() {
    if (!NAME_RE.test(name) || name.length > 63) {
      setError("A token's name is a DNS label: lowercase letters, digits and '-'")
      return
    }
    const t = await run(() => tokens.create({ user: own ? undefined : user, name, description: description.trim() || undefined, expiresIn }))
    if (t) {
      setMade({ name: t.name, token: t.token })
      setName('')
      setDescription('')
      load()
    }
  }

  return (
    <Modal title={own ? 'Your API tokens' : `${user}'s API tokens`} onClose={onClose} wide>
      <p className="mb-4 text-sm text-neutral-500">
        A token signs in as {own ? 'you' : user}, with {own ? 'your' : 'their'} access, for scripts and CI:{' '}
        <code>hyve login URL --token-stdin</code>, or <code>HYVE_URL</code> and <code>HYVE_TOKEN</code> in the environment. A password change doesn't
        revoke it; {own ? '' : 'disabling the user or '}revoking it here does.
      </p>

      {made && (
        <div className="mb-4 rounded-lg border border-green-300 bg-green-50 p-3 dark:border-green-800 dark:bg-green-950/40">
          <p className="mb-2 text-sm font-medium text-green-800 dark:text-green-300">Token {made.name} made. Copy it now: it isn't shown again.</p>
          <code className="mb-2 block rounded-lg bg-white p-2 font-mono text-xs break-all text-neutral-800 select-all dark:bg-neutral-950 dark:text-neutral-200">
            {made.token}
          </code>
          <div className="flex justify-end gap-2">
            <button
              type="button"
              className="btn btn-sm"
              onClick={() =>
                navigator.clipboard.writeText(made.token).then(
                  () => setCopied(true),
                  () => setError('No clipboard here; select the token and copy it.'),
                )
              }
            >
              {copied ? 'Copied' : 'Copy'}
            </button>
            <button
              type="button"
              className="btn btn-tertiary btn-sm"
              onClick={() => {
                setMade(null)
                setCopied(false)
              }}
            >
              Done
            </button>
          </div>
        </div>
      )}

      <ErrorBanner error={loadError} />
      {list === null && !loadError ? (
        <Loading />
      ) : list && list.length === 0 ? (
        <EmptyState>No API tokens.</EmptyState>
      ) : (
        list && (
          <div className="mb-4 overflow-x-auto">
            <table className="w-full text-sm">
              <thead>
                <tr className="text-left text-xs text-neutral-500 uppercase">
                  <th className="py-2 pr-3 font-medium">Name</th>
                  <th className="py-2 pr-3 font-medium">Expires</th>
                  <th className="py-2 pr-3 font-medium">Last used</th>
                  <th className="py-2 pr-3 font-medium">Made by</th>
                  <th />
                </tr>
              </thead>
              <tbody className="divide-y divide-neutral-100 dark:divide-neutral-800">
                {list.map((t) => (
                  <tr key={t.id}>
                    <td className="py-2 pr-3">
                      <div className="font-medium text-neutral-900 dark:text-neutral-100">{t.name}</div>
                      {t.description && <div className="text-xs text-neutral-500">{t.description}</div>}
                    </td>
                    <td className="py-2 pr-3 whitespace-nowrap">{day(t.expiresAt)}</td>
                    <td className="py-2 pr-3 whitespace-nowrap">{t.lastUsed ? day(t.lastUsed) : 'Never'}</td>
                    <td className="py-2 pr-3">{t.createdBy || '-'}</td>
                    <td className="py-2 text-right">
                      <ConfirmButton
                        label="Revoke"
                        confirm={`Revoke ${t.name}?`}
                        onConfirm={() =>
                          run(async () => {
                            await tokens.revoke(t.user, t.name)
                            if (made?.name === t.name) setMade(null)
                            load()
                          })
                        }
                      />
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )
      )}

      <form
        className="border-t border-neutral-100 pt-4 dark:border-neutral-800"
        onSubmit={(e) => {
          e.preventDefault()
          create()
        }}
      >
        <h3 className="mb-3 text-sm font-semibold">New token</h3>
        <div className="mb-3 grid gap-3 sm:grid-cols-2">
          <TextField label="Name" value={name} onChange={setName} placeholder="github-actions" />
          <SelectField label="Expires in" value={expiresIn} onChange={setExpiresIn} options={EXPIRIES} />
        </div>
        <TextField className="mb-3" label="Description" value={description} onChange={setDescription} placeholder="What it's for" />
        <ErrorBanner error={error} />
        <div className="flex justify-end gap-2">
          <button type="button" className="btn btn-tertiary" onClick={onClose}>
            Close
          </button>
          <button type="submit" className="btn btn-primary" disabled={busy || !name}>
            {busy ? 'Making…' : 'Make token'}
          </button>
        </div>
      </form>
    </Modal>
  )
}
