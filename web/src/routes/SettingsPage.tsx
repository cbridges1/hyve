import { useEffect, useState } from 'react'
import { Card } from '../components/Card'
import { configApi } from '../lib/api/config'
import { ApiError } from '../lib/api/client'
import type { HyveConfig, ImageInstall } from '../lib/api/types'
import { useApi } from '../lib/useApi'

const inputClass = 'w-full rounded-lg border border-neutral-300 px-2.5 py-1.5 text-sm dark:border-neutral-700 dark:bg-neutral-800'

const emptyForm: Omit<HyveConfig, 'exists'> = {
  strictResourceDelete: false,
  defaultWorkflowImage: '',
  defaultModuleImage: '',
  defaultAgentImage: '',
  imageInstalls: [],
  imagePullSecrets: [],
}

/** Strips out blank rows a caller left half-filled rather than deleted with the ✕ button — an empty string image/secret is never meaningful. */
function cleanForm(form: Omit<HyveConfig, 'exists'>): Omit<HyveConfig, 'exists'> {
  return {
    ...form,
    imagePullSecrets: (form.imagePullSecrets ?? []).map((s) => s.trim()).filter(Boolean),
    imageInstalls: (form.imageInstalls ?? []).filter((ii) => ii.image.trim() && ii.install.trim()),
  }
}

export function SettingsPage() {
  const { data: config, loading, error, reload } = useApi(() => configApi.get())
  const [form, setForm] = useState<Omit<HyveConfig, 'exists'>>(emptyForm)
  const [saving, setSaving] = useState(false)
  const [saveError, setSaveError] = useState<string | null>(null)
  const [saved, setSaved] = useState(false)

  // Only overwrite local edits when a genuinely new server snapshot lands
  // (on mount, and after this page's own save re-fetches) — not on every
  // background re-render, which would otherwise clobber whatever the user
  // is mid-typing.
  useEffect(() => {
    if (config) setForm({ ...emptyForm, ...config })
  }, [config])

  async function onSave() {
    setSaving(true)
    setSaveError(null)
    setSaved(false)
    try {
      await configApi.update(cleanForm(form))
      setSaved(true)
      reload()
    } catch (err) {
      setSaveError(err instanceof ApiError ? err.message : 'Failed to save config')
    } finally {
      setSaving(false)
    }
  }

  function updateImageInstall(i: number, patch: Partial<ImageInstall>) {
    setForm((f) => ({
      ...f,
      imageInstalls: (f.imageInstalls ?? []).map((ii, idx) => (idx === i ? { ...ii, ...patch } : ii)),
    }))
  }

  function updatePullSecret(i: number, value: string) {
    setForm((f) => ({
      ...f,
      imagePullSecrets: (f.imagePullSecrets ?? []).map((s, idx) => (idx === i ? value : s)),
    }))
  }

  return (
    <div className="space-y-4">
      <div>
        <h1 className="text-lg font-semibold text-neutral-900 dark:text-neutral-100">Settings</h1>
        <p className="mt-0.5 text-sm text-neutral-500">
          The install-wide <code className="rounded bg-neutral-100 px-1 dark:bg-neutral-800">HyveConfig</code> singleton —
          superadmin-only, applies across every tenant. {config && !config.exists && 'Not created yet — saving will create it.'}
        </p>
      </div>

      {loading && <p className="text-sm text-neutral-500">Loading…</p>}
      {error && <p className="text-sm text-red-600 dark:text-red-400">{error}</p>}

      <Card title="Defaults">
        <label className="flex items-center gap-2 text-sm text-neutral-800 dark:text-neutral-200">
          <input
            type="checkbox"
            checked={form.strictResourceDelete}
            onChange={(e) => setForm((f) => ({ ...f, strictResourceDelete: e.target.checked }))}
            className="h-4 w-4 rounded border-neutral-300 dark:border-neutral-700"
          />
          Strict resource delete
          <span className="text-xs text-neutral-400">— auto-prune a removed spec.resources entry instead of just warning</span>
        </label>

        <div className="mt-4 grid gap-3 sm:grid-cols-3">
          <label className="text-sm">
            <span className="mb-1 block text-neutral-600 dark:text-neutral-400">Default workflow image</span>
            <input
              value={form.defaultWorkflowImage ?? ''}
              onChange={(e) => setForm((f) => ({ ...f, defaultWorkflowImage: e.target.value }))}
              placeholder="e.g. alpine/k8s:1.30"
              className={inputClass}
            />
          </label>
          <label className="text-sm">
            <span className="mb-1 block text-neutral-600 dark:text-neutral-400">Default module image</span>
            <input
              value={form.defaultModuleImage ?? ''}
              onChange={(e) => setForm((f) => ({ ...f, defaultModuleImage: e.target.value }))}
              placeholder="e.g. alpine/k8s:1.30"
              className={inputClass}
            />
          </label>
          <label className="text-sm">
            <span className="mb-1 block text-neutral-600 dark:text-neutral-400">Default agent image</span>
            <input
              value={form.defaultAgentImage ?? ''}
              onChange={(e) => setForm((f) => ({ ...f, defaultAgentImage: e.target.value }))}
              placeholder="empty = controller's own built-in default"
              className={inputClass}
            />
          </label>
        </div>
      </Card>

      <Card
        title="Image pull secrets"
        action={
          <button
            type="button"
            onClick={() => setForm((f) => ({ ...f, imagePullSecrets: [...(f.imagePullSecrets ?? []), ''] }))}
            className="rounded-lg px-2.5 py-1 text-xs font-medium text-neutral-600 transition-colors hover:bg-neutral-100 dark:text-neutral-400 dark:hover:bg-neutral-800"
          >
            + Add
          </button>
        }
      >
        <p className="mb-2 text-xs text-neutral-500">
          Names of existing <code className="rounded bg-neutral-100 px-1 dark:bg-neutral-800">kubernetes.io/dockerconfigjson</code>{' '}
          Secrets (in the controller's own namespace) — this doesn't create them.
        </p>
        {(form.imagePullSecrets ?? []).length === 0 && <p className="text-sm text-neutral-400">None configured.</p>}
        <div className="space-y-2">
          {(form.imagePullSecrets ?? []).map((secret, i) => (
            <div key={i} className="flex gap-2">
              <input
                value={secret}
                onChange={(e) => updatePullSecret(i, e.target.value)}
                placeholder="secret name"
                className={inputClass}
              />
              <button
                type="button"
                onClick={() => setForm((f) => ({ ...f, imagePullSecrets: (f.imagePullSecrets ?? []).filter((_, idx) => idx !== i) }))}
                className="shrink-0 rounded-lg px-2.5 text-sm text-red-600 transition-colors hover:bg-red-50 dark:text-red-400 dark:hover:bg-red-950/40"
              >
                ✕
              </button>
            </div>
          ))}
        </div>
      </Card>

      <Card
        title="Image installs"
        action={
          <button
            type="button"
            onClick={() => setForm((f) => ({ ...f, imageInstalls: [...(f.imageInstalls ?? []), { image: '', install: '' }] }))}
            className="rounded-lg px-2.5 py-1 text-xs font-medium text-neutral-600 transition-colors hover:bg-neutral-100 dark:text-neutral-400 dark:hover:bg-neutral-800"
          >
            + Add
          </button>
        }
      >
        <p className="mb-2 text-xs text-neutral-500">
          Per exact image reference, a shell script run once at the start of every job dispatched with that image.
        </p>
        {(form.imageInstalls ?? []).length === 0 && <p className="text-sm text-neutral-400">None configured.</p>}
        <div className="space-y-3">
          {(form.imageInstalls ?? []).map((ii, i) => (
            <div key={i} className="space-y-1.5 rounded-lg border border-neutral-200 p-3 dark:border-neutral-800">
              <div className="flex gap-2">
                <input
                  value={ii.image}
                  onChange={(e) => updateImageInstall(i, { image: e.target.value })}
                  placeholder="image reference, e.g. alpine:3"
                  className={inputClass}
                />
                <button
                  type="button"
                  onClick={() => setForm((f) => ({ ...f, imageInstalls: (f.imageInstalls ?? []).filter((_, idx) => idx !== i) }))}
                  className="shrink-0 rounded-lg px-2.5 text-sm text-red-600 transition-colors hover:bg-red-50 dark:text-red-400 dark:hover:bg-red-950/40"
                >
                  ✕
                </button>
              </div>
              <textarea
                value={ii.install}
                onChange={(e) => updateImageInstall(i, { install: e.target.value })}
                placeholder="install script, e.g. apk add curl"
                rows={8}
                className={`${inputClass} resize-y font-mono text-xs`}
              />
            </div>
          ))}
        </div>
      </Card>

      <div className="flex items-center gap-3">
        <button
          type="button"
          onClick={onSave}
          disabled={saving}
          className="rounded-lg bg-neutral-900 px-4 py-2 text-sm font-medium text-white transition-colors hover:bg-neutral-800 disabled:opacity-50 dark:bg-white dark:text-neutral-900 dark:hover:bg-neutral-200"
        >
          {saving ? 'Saving…' : 'Save'}
        </button>
        {saved && <span className="text-sm text-green-700 dark:text-green-400">Saved.</span>}
        {saveError && <span className="text-sm text-red-600 dark:text-red-400">{saveError}</span>}
      </div>
    </div>
  )
}
