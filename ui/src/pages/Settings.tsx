// The host's settings: for now its email, the SMTP relay that sends
// password resets and account emails (created, password, address or groups
// changed). Install-wide; reading it takes get, saving it update, on the
// hyve-email Secret in hyve's namespace.
import { useEffect, useState } from 'react'
import { Checkbox, TextField } from '../components/form'
import { Banner, Card, ErrorBanner, Loading, PageHeader } from '../components/ui'
import { emailSettings, errorMessage, type EmailSettings, type EmailSettingsUpdate } from '../lib/api'
import { useSession } from '../lib/session'

type Form = { host: string; port: string; username: string; password: string; useTLS: boolean; skipVerify: boolean; fromAddress: string; fromName: string }

const toForm = (s: EmailSettings): Form => ({
  host: s.host,
  port: String(s.port || 587),
  username: s.username,
  password: '', // never sent back: blank keeps it
  useTLS: s.useTLS,
  skipVerify: s.skipVerify,
  fromAddress: s.fromAddress,
  fromName: s.fromName,
})

export default function Settings() {
  const me = useSession().user
  const [settings, setSettings] = useState<EmailSettings | null>(null)
  const [form, setForm] = useState<Form | null>(null)
  const [loadError, setLoadError] = useState<unknown>(null)
  const [saving, setSaving] = useState(false)
  const [result, setResult] = useState<{ ok: boolean; message: string } | null>(null)
  const [clearPassword, setClearPassword] = useState(false)
  const [testTo, setTestTo] = useState('')
  const [testing, setTesting] = useState(false)
  const [testResult, setTestResult] = useState<{ ok: boolean; message: string } | null>(null)

  useEffect(() => {
    emailSettings.get().then((s) => {
      setSettings(s)
      setForm(toForm(s))
    }, setLoadError)
  }, [])

  const set = (patch: Partial<Form>) => {
    setForm((f) => (f ? { ...f, ...patch } : f))
    setResult(null)
  }

  async function save() {
    if (!form) return
    const port = Number(form.port)
    if (!Number.isInteger(port) || port < 1 || port > 65535) {
      setResult({ ok: false, message: 'The port must be a number from 1 to 65535.' })
      return
    }
    const body: EmailSettingsUpdate = {
      host: form.host.trim(),
      port,
      username: form.username.trim(),
      useTLS: form.useTLS,
      skipVerify: form.skipVerify,
      fromAddress: form.fromAddress.trim(),
      fromName: form.fromName.trim(),
    }
    // Blank keeps the saved password; removing it is its own choice.
    if (form.password) body.password = form.password
    else if (clearPassword) body.password = ''
    setSaving(true)
    setResult(null)
    try {
      const s = await emailSettings.update(body)
      setSettings(s)
      setForm(toForm(s))
      setClearPassword(false)
      setResult({ ok: true, message: 'Saved.' })
    } catch (e) {
      setResult({ ok: false, message: errorMessage(e) })
    } finally {
      setSaving(false)
    }
  }

  async function test() {
    setTesting(true)
    setTestResult(null)
    try {
      await emailSettings.test(testTo.trim())
      setTestResult({ ok: true, message: `Sent a test email to ${testTo.trim()}.` })
    } catch (e) {
      setTestResult({ ok: false, message: errorMessage(e) })
    } finally {
      setTesting(false)
    }
  }

  return (
    <div className="max-w-3xl space-y-4">
      <PageHeader title="Settings" />
      {loadError ? (
        <ErrorBanner error={loadError} />
      ) : !form ? (
        <Loading />
      ) : (
        <>
          <Card title="Email">
            <p className="text-muted mt-0 mb-4 text-sm">
              The SMTP relay hyve sends password resets and account emails through: an account was created, or its password, email address or
              groups changed. Users get them at the email address on their user.
            </p>
            {!settings?.configured && (
              <Banner tone="info">
                Email isn't set up. Until it is, a password reset's code is written to the host's log instead (
                <code>kubectl logs -n hyve-system deploy/hyve-host</code>).
              </Banner>
            )}
            <div className="mt-4 grid gap-3 sm:grid-cols-[1fr_8rem]">
              <TextField label="SMTP host" value={form.host} onChange={(v) => set({ host: v })} placeholder="smtp.example.com" />
              <TextField label="Port" value={form.port} onChange={(v) => set({ port: v.replace(/\D/g, '') })} placeholder="587" />
            </div>
            <div className="mt-3 grid gap-3 sm:grid-cols-2">
              <TextField
                label="Username"
                value={form.username}
                onChange={(v) => set({ username: v })}
                autoComplete="off"
                help="Leave blank for an anonymous relay."
              />
              <TextField
                label="Password"
                type="password"
                value={form.password}
                onChange={(v) => set({ password: v })}
                autoComplete="new-password"
                placeholder={settings?.passwordSet && !clearPassword ? '•••••••• (saved)' : ''}
                help={settings?.passwordSet ? 'Leave blank to keep the saved one.' : undefined}
              />
              <TextField label="From address" value={form.fromAddress} onChange={(v) => set({ fromAddress: v })} placeholder="no-reply@example.com" />
              <TextField label="From name" value={form.fromName} onChange={(v) => set({ fromName: v })} placeholder="hyve" />
            </div>
            <div className="mt-4 space-y-2">
              <Checkbox
                checked={form.useTLS}
                onChange={(v) => set({ useTLS: v, ...(v && form.port === '587' ? { port: '465' } : !v && form.port === '465' ? { port: '587' } : {}) })}
                label={
                  <>
                    Implicit TLS <span className="text-muted text-xs">— usually port 465; otherwise STARTTLS when the server offers it, usually 587</span>
                  </>
                }
              />
              <Checkbox
                checked={form.skipVerify}
                onChange={(v) => set({ skipVerify: v })}
                label={
                  <>
                    Skip certificate verification <span className="text-muted text-xs">— only for an internal relay with a self-signed certificate</span>
                  </>
                }
              />
              {settings?.passwordSet && (
                <Checkbox
                  checked={clearPassword}
                  onChange={(v) => {
                    setClearPassword(v)
                    if (v) set({ password: '' })
                  }}
                  label="Remove the saved password"
                />
              )}
            </div>
            <div className="mt-5 flex items-center gap-3">
              <button type="button" className="btn btn-primary" disabled={saving} onClick={save}>
                {saving ? 'Saving…' : 'Save'}
              </button>
              {result && <span className={`text-sm ${result.ok ? 'text-green-700 dark:text-green-400' : 'text-red-600 dark:text-red-400'}`}>{result.message}</span>}
            </div>
          </Card>

          <Card title="Send a test email">
            <p className="text-muted mt-0 mb-3 text-sm">Sends with the saved settings, and shows the relay's error if it fails.</p>
            <div className="flex flex-col gap-2 sm:flex-row">
              <input
                type="email"
                className="sm:max-w-xs"
                value={testTo}
                placeholder={me?.username ? 'you@example.com' : ''}
                onChange={(e) => setTestTo(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === 'Enter' && testTo.trim() && !testing) test()
                }}
              />
              <button type="button" className="btn btn-secondary shrink-0" disabled={!testTo.trim() || testing || !settings?.configured} onClick={test}>
                {testing ? 'Sending…' : 'Send test email'}
              </button>
            </div>
            {!settings?.configured && <p className="hint mt-2">Save the SMTP settings first.</p>}
            {testResult && (
              <p className={`mt-2 mb-0 text-sm break-words ${testResult.ok ? 'text-green-700 dark:text-green-400' : 'text-red-600 dark:text-red-400'}`}>
                {testResult.message}
              </p>
            )}
          </Card>
        </>
      )}
    </div>
  )
}
