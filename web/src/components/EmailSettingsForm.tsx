import { useEffect, useState } from 'react'
import { Card } from './Card'
import { ApiError } from '../lib/api/client'
import { emailSettingsApi, type EmailSettings, type UpdateEmailSettingsRequest } from '../lib/api/emailSettings'
import { useApi } from '../lib/useApi'

const inputClass = 'w-full rounded-lg border border-neutral-300 px-2.5 py-1.5 text-sm dark:border-neutral-700 dark:bg-neutral-800'

type FormState = {
  smtpHost: string
  smtpPort: number
  smtpUsername: string
  smtpPassword: string
  useTls: boolean
  skipVerify: boolean
  fromAddress: string
  fromName: string
}

const emptyForm: FormState = {
  smtpHost: '',
  smtpPort: 587,
  smtpUsername: '',
  smtpPassword: '',
  useTls: false,
  skipVerify: false,
  fromAddress: '',
  fromName: '',
}

function toFormState(s: EmailSettings): FormState {
  return {
    smtpHost: s.smtpHost,
    smtpPort: s.smtpPort,
    smtpUsername: s.smtpUsername ?? '',
    smtpPassword: '', // never echoed by the API — a blank field means "leave unchanged" on save
    useTls: s.useTls,
    skipVerify: s.skipVerify,
    fromAddress: s.fromAddress,
    fromName: s.fromName ?? '',
  }
}

// EmailSettingsForm is the install-wide SMTP configuration surface —
// superadmin-only, rendered inside SettingsPage alongside the existing
// HyveConfigForm (both are install-wide, control-plane-only settings, so
// no separate nav entry — see HYVE-EMAIL-IMPLEMENTATION-PLAN.md's
// Milestone 3).
export function EmailSettingsForm() {
  const { data: settings, loading, error, reload } = useApi(() => emailSettingsApi.get())
  const [form, setForm] = useState<FormState>(emptyForm)
  const [saving, setSaving] = useState(false)
  const [saveError, setSaveError] = useState<string | null>(null)
  const [saved, setSaved] = useState(false)

  const [testTo, setTestTo] = useState('')
  const [testing, setTesting] = useState(false)
  const [testResult, setTestResult] = useState<{ ok: boolean; message: string } | null>(null)

  useEffect(() => {
    if (settings) setForm(toFormState(settings))
  }, [settings])

  async function onSaveClick() {
    setSaving(true)
    setSaveError(null)
    setSaved(false)
    try {
      const body: UpdateEmailSettingsRequest = {
        smtpHost: form.smtpHost,
        smtpPort: form.smtpPort,
        smtpUsername: form.smtpUsername,
        useTls: form.useTls,
        skipVerify: form.skipVerify,
        fromAddress: form.fromAddress,
        fromName: form.fromName,
      }
      // Omit smtpPassword entirely when the field is blank — leaves the
      // stored password untouched, per the API's own nil-vs-empty
      // convention. A caller who genuinely wants to clear it has no UI
      // path to do that from a blank field here (there'd be no way to
      // distinguish "didn't touch it" from "wants it cleared"); that's
      // fine — going back to an anonymous relay is rare enough not to
      // need a dedicated control.
      if (form.smtpPassword !== '') body.smtpPassword = form.smtpPassword
      await emailSettingsApi.update(body)
      setSaved(true)
      setForm((f) => ({ ...f, smtpPassword: '' }))
      reload()
    } catch (err) {
      setSaveError(err instanceof ApiError ? err.message : 'Failed to save email settings')
    } finally {
      setSaving(false)
    }
  }

  async function onTestClick() {
    setTesting(true)
    setTestResult(null)
    try {
      await emailSettingsApi.test(testTo)
      setTestResult({ ok: true, message: `Test email sent to ${testTo}.` })
    } catch (err) {
      setTestResult({ ok: false, message: err instanceof ApiError ? err.message : 'Failed to send test email' })
    } finally {
      setTesting(false)
    }
  }

  return (
    <div className="space-y-4">
      <div>
        <h2 className="text-base font-semibold text-neutral-900 dark:text-neutral-100">Email</h2>
        <p className="mt-0.5 text-sm text-neutral-500">
          Install-wide SMTP settings — superadmin-only, used for password resets and account notifications across every
          tenant.
        </p>
      </div>

      {loading && <p className="text-sm text-neutral-500">Loading…</p>}
      {error && <p className="text-sm text-red-600 dark:text-red-400">{error}</p>}

      <Card title="SMTP">
        <div className="grid gap-3 sm:grid-cols-2">
          <label className="text-sm">
            <span className="mb-1 block text-neutral-600 dark:text-neutral-400">Host</span>
            <input
              value={form.smtpHost}
              onChange={(e) => setForm((f) => ({ ...f, smtpHost: e.target.value }))}
              placeholder="smtp.example.com"
              className={inputClass}
            />
          </label>
          <label className="text-sm">
            <span className="mb-1 block text-neutral-600 dark:text-neutral-400">Port</span>
            <input
              type="number"
              value={form.smtpPort}
              onChange={(e) => setForm((f) => ({ ...f, smtpPort: Number(e.target.value) }))}
              className={inputClass}
            />
          </label>
          <label className="text-sm">
            <span className="mb-1 block text-neutral-600 dark:text-neutral-400">Username</span>
            <input
              value={form.smtpUsername}
              onChange={(e) => setForm((f) => ({ ...f, smtpUsername: e.target.value }))}
              placeholder="leave blank for an anonymous relay"
              autoComplete="off"
              className={inputClass}
            />
          </label>
          <label className="text-sm">
            <span className="mb-1 block text-neutral-600 dark:text-neutral-400">
              Password{settings?.passwordSet && <span className="text-xs text-neutral-400"> — leave blank to keep current</span>}
            </span>
            <input
              type="password"
              value={form.smtpPassword}
              onChange={(e) => setForm((f) => ({ ...f, smtpPassword: e.target.value }))}
              autoComplete="new-password"
              className={inputClass}
            />
          </label>
          <label className="text-sm">
            <span className="mb-1 block text-neutral-600 dark:text-neutral-400">From address</span>
            <input
              value={form.fromAddress}
              onChange={(e) => setForm((f) => ({ ...f, fromAddress: e.target.value }))}
              placeholder="no-reply@example.com"
              className={inputClass}
            />
          </label>
          <label className="text-sm">
            <span className="mb-1 block text-neutral-600 dark:text-neutral-400">From name</span>
            <input
              value={form.fromName}
              onChange={(e) => setForm((f) => ({ ...f, fromName: e.target.value }))}
              placeholder="hyve"
              className={inputClass}
            />
          </label>
        </div>

        <div className="mt-4 flex flex-col gap-2 sm:flex-row sm:gap-6">
          <label className="flex items-center gap-2 text-sm text-neutral-800 dark:text-neutral-200">
            <input
              type="checkbox"
              checked={form.useTls}
              onChange={(e) => setForm((f) => ({ ...f, useTls: e.target.checked }))}
              className="h-4 w-4 rounded border-neutral-300 dark:border-neutral-700"
            />
            Implicit TLS
            <span className="text-xs text-neutral-400">— typically port 465; unchecked uses STARTTLS on connect, typically port 587</span>
          </label>
          <label className="flex items-center gap-2 text-sm text-neutral-800 dark:text-neutral-200">
            <input
              type="checkbox"
              checked={form.skipVerify}
              onChange={(e) => setForm((f) => ({ ...f, skipVerify: e.target.checked }))}
              className="h-4 w-4 rounded border-neutral-300 dark:border-neutral-700"
            />
            Skip certificate verification
            <span className="text-xs text-neutral-400">— only for an internal relay with a self-signed certificate</span>
          </label>
        </div>

        <div className="mt-5 flex items-center gap-3">
          <button
            type="button"
            onClick={onSaveClick}
            disabled={saving}
            className="rounded-lg bg-neutral-900 px-4 py-2 text-sm font-medium text-white transition-colors hover:bg-neutral-800 disabled:opacity-50 dark:bg-white dark:text-neutral-900 dark:hover:bg-neutral-200"
          >
            {saving ? 'Saving…' : 'Save'}
          </button>
          {saved && <span className="text-sm text-green-700 dark:text-green-400">Saved.</span>}
          {saveError && <span className="text-sm text-red-600 dark:text-red-400">{saveError}</span>}
        </div>
      </Card>

      <Card title="Send a test email">
        <div className="flex flex-col gap-2 sm:flex-row">
          <input
            type="email"
            value={testTo}
            onChange={(e) => setTestTo(e.target.value)}
            placeholder="you@example.com"
            className={`${inputClass} sm:max-w-xs`}
          />
          <button
            type="button"
            disabled={!testTo || testing}
            onClick={onTestClick}
            className="shrink-0 rounded-lg border border-neutral-300 px-3.5 py-2 text-sm font-medium text-neutral-700 transition-colors hover:bg-neutral-100 disabled:opacity-50 dark:border-neutral-700 dark:text-neutral-300 dark:hover:bg-neutral-800"
          >
            {testing ? 'Sending…' : 'Send test email'}
          </button>
        </div>
        {testResult && (
          <p className={`mt-2 text-sm ${testResult.ok ? 'text-green-700 dark:text-green-400' : 'text-red-600 dark:text-red-400'}`}>
            {testResult.message}
          </p>
        )}
      </Card>
    </div>
  )
}
