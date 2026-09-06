import { useState } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import { BackLink, Card, CodeBlock, Field } from '../components/Card'
import { AdminOnly } from '../components/RoleGate'
import { SpecEditor } from '../components/SpecEditor'
import { accessMethodsApi } from '../lib/api/accessMethods'
import { ApiError } from '../lib/api/client'
import { useConfirm } from '../lib/confirm'
import { useApi } from '../lib/useApi'

const inputClass = 'w-full rounded-lg border border-neutral-300 px-2.5 py-1.5 text-sm dark:border-neutral-700 dark:bg-neutral-800'

function MintPanel({ name, requiredEnv }: { name: string; requiredEnv: string[] }) {
  const [clusterName, setClusterName] = useState('')
  const [accessMethodClusterID, setAccessMethodClusterID] = useState('')
  const [envValues, setEnvValues] = useState<Record<string, string>>({})
  const [kubeconfig, setKubeconfig] = useState<string | null>(null)
  const [copied, setCopied] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [submitting, setSubmitting] = useState(false)

  async function mint() {
    setError(null)
    setKubeconfig(null)
    setSubmitting(true)
    try {
      const res = await accessMethodsApi.mint(name, {
        clusterName,
        accessMethodClusterID,
        credentialEnv: envValues,
      })
      setKubeconfig(res.kubeconfig)
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Failed to mint kubeconfig')
    } finally {
      setSubmitting(false)
    }
  }

  async function copy() {
    if (!kubeconfig) return
    await navigator.clipboard.writeText(kubeconfig)
    setCopied(true)
    setTimeout(() => setCopied(false), 1500)
  }

  const canSubmit = !!clusterName && !!accessMethodClusterID

  return (
    <Card title="Mint kubeconfig">
      <p className="mb-3 text-xs text-neutral-500">
        Dispatches this access method's auth operation server-side, inside an ephemeral job, and returns the resulting
        kubeconfig — the same thing <code>hyve cluster auth</code> does, just from the browser. Credentials below are
        sent once for this request only, never stored.
      </p>
      <div className="mb-3 grid grid-cols-1 gap-3 sm:grid-cols-2">
        <label className="text-sm">
          <span className="mb-1 block text-neutral-600 dark:text-neutral-400">Cluster name</span>
          <input value={clusterName} onChange={(e) => setClusterName(e.target.value)} className={inputClass} />
        </label>
        <label className="text-sm">
          <span className="mb-1 block text-neutral-600 dark:text-neutral-400">Access method cluster ID</span>
          <input
            value={accessMethodClusterID}
            onChange={(e) => setAccessMethodClusterID(e.target.value)}
            placeholder="e.g. Rancher's own cluster ID"
            className={inputClass}
          />
        </label>
      </div>

      {requiredEnv.length > 0 && (
        <div className="mb-3 space-y-2">
          {requiredEnv.map((key) => (
            <label key={key} className="block text-sm">
              <span className="mb-1 block font-mono text-xs text-neutral-600 dark:text-neutral-400">{key}</span>
              <input
                type="password"
                value={envValues[key] ?? ''}
                onChange={(e) => setEnvValues({ ...envValues, [key]: e.target.value })}
                autoComplete="off"
                className={inputClass}
              />
            </label>
          ))}
        </div>
      )}

      {error && <p className="mb-3 text-sm text-red-600 dark:text-red-400">{error}</p>}

      <button
        type="button"
        disabled={!canSubmit || submitting}
        onClick={mint}
        className="rounded-lg bg-neutral-900 px-3.5 py-2 text-sm font-medium text-white transition-colors hover:bg-neutral-800 disabled:opacity-50 dark:bg-white dark:text-neutral-900 dark:hover:bg-neutral-200"
      >
        {submitting ? 'Minting…' : 'Mint kubeconfig'}
      </button>

      {kubeconfig && (
        <div className="mt-4">
          <div className="mb-1.5 flex items-center justify-between">
            <span className="text-xs font-medium tracking-wide text-neutral-500 uppercase dark:text-neutral-500">
              Kubeconfig
            </span>
            <button
              type="button"
              onClick={copy}
              className="rounded-lg px-2 py-1 text-xs font-medium text-neutral-600 transition-colors hover:bg-neutral-100 dark:text-neutral-400 dark:hover:bg-neutral-700"
            >
              {copied ? 'Copied' : 'Copy'}
            </button>
          </div>
          <CodeBlock>{kubeconfig}</CodeBlock>
        </div>
      )}
    </Card>
  )
}

export function AccessMethodDetailPage() {
  const { name = '' } = useParams()
  const navigate = useNavigate()
  const confirm = useConfirm()
  const { data: am, loading, error, reload } = useApi(() => accessMethodsApi.get(name), [name])
  const [deleteError, setDeleteError] = useState<string | null>(null)

  async function onDelete() {
    const ok = await confirm({
      title: `Delete access method "${name}"?`,
      message: 'This cannot be undone. Any cluster still referencing this access method by name will fail to resolve it.',
      confirmLabel: 'Delete',
      danger: true,
    })
    if (!ok) return
    setDeleteError(null)
    try {
      await accessMethodsApi.delete(name)
      navigate('/access-methods')
    } catch (err) {
      setDeleteError(err instanceof ApiError ? err.message : 'Failed to delete access method')
    }
  }

  if (loading) return <p className="text-sm text-neutral-500">Loading…</p>
  if (error || !am) return <p className="text-sm text-red-600 dark:text-red-400">{error ?? 'Not found'}</p>

  return (
    <div className="space-y-4">
      <BackLink to="/access-methods" label="Access methods" />

      <div className="flex items-start justify-between gap-3">
        <h1 className="truncate text-lg font-semibold text-neutral-900 dark:text-neutral-100">{am.name}</h1>
        <AdminOnly>
          <button
            type="button"
            onClick={onDelete}
            className="shrink-0 rounded-lg border border-red-200 px-3 py-1.5 text-sm font-medium text-red-700 transition-colors hover:bg-red-50 dark:border-red-900/60 dark:text-red-400 dark:hover:bg-red-950/40"
          >
            Delete
          </button>
        </AdminOnly>
      </div>
      {deleteError && <p className="text-sm text-red-600 dark:text-red-400">{deleteError}</p>}

      <Card title="Overview">
        <Field label="Server URL">{am.spec.serverURL}</Field>
        {am.spec.driver?.source && (
          <Field label="Driver">
            {am.spec.driver.source}
            {am.spec.driver.version ? `@${am.spec.driver.version}` : ''}
          </Field>
        )}
        {am.spec.inlineAuth && <Field label="Auth">inline script (see below)</Field>}
        {am.spec.runner?.image && <Field label="Runner image">{am.spec.runner.image}</Field>}
        <Field label="Required credentials">
          {am.requiredEnv && am.requiredEnv.length > 0 ? am.requiredEnv.join(', ') : 'none declared'}
        </Field>
      </Card>

      {am.spec.inlineAuth && (
        <Card title="Inline auth script">
          <CodeBlock>{am.spec.inlineAuth}</CodeBlock>
        </Card>
      )}

      <AdminOnly>
        <SpecEditor spec={am.spec} onSave={(spec) => accessMethodsApi.update(name, spec).then(reload)} />
      </AdminOnly>

      <MintPanel name={am.name} requiredEnv={am.requiredEnv ?? []} />
    </div>
  )
}
