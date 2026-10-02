import { EmailSettingsForm } from '../components/EmailSettingsForm'
import { HyveConfigForm } from '../components/HyveConfigForm'
import { configApi } from '../lib/api/config'
import { useApi } from '../lib/useApi'

export function SettingsPage() {
  const { data: config, loading, error, reload } = useApi(() => configApi.get())

  return (
    <div className="space-y-8">
      <HyveConfigForm
        title="Settings"
        description={
          <>
            The install-wide <code className="rounded bg-neutral-100 px-1 dark:bg-neutral-800">HyveConfig</code> singleton — superadmin-only,
            applies across every tenant on the control plane's own home cluster.
          </>
        }
        config={config}
        loading={loading}
        error={error}
        onSave={(form) => configApi.update(form)}
        onSaved={reload}
      />
      <EmailSettingsForm />
    </div>
  )
}
