import { Card } from '../components/Card'
import { HyveConfigForm } from '../components/HyveConfigForm'
import { orgConfigApi } from '../lib/api/orgConfig'
import { useApi } from '../lib/useApi'
import { useActAs } from '../lib/useActAs'
import { useWhoami } from '../lib/useWhoami'

// OrgSettingsPage is the organization-admin-facing counterpart to
// SettingsPage (the control plane's own home-cluster HyveConfig singleton,
// superadmin-only) — reachable by an ordinary admin for their own
// organization, or a superadmin currently "Viewing" one, once that
// organization has a reconciling cluster of its own (see
// AppShell's own nav-gating: an organization still on the shared home
// cluster has no namespace-scoped HyveConfig here to configure at all).
export function OrgSettingsPage() {
  const who = useWhoami().data
  const [actAs] = useActAs()
  const orgName = actAs ?? who?.namespace ?? ''

  const { data: config, loading, error, reload } = useApi(
    () => (orgName ? orgConfigApi.get(orgName) : Promise.resolve(null)),
    [orgName],
  )

  if (who && !who.reconcilingCluster) {
    return (
      <div className="space-y-4">
        <div>
          <h1 className="text-lg font-semibold text-neutral-900 dark:text-neutral-100">Settings</h1>
        </div>
        <Card>
          <p className="text-sm text-neutral-700 dark:text-neutral-300">
            This organization is still on this install's own shared home cluster, which has no settings of its own to
            configure here — register a dedicated reconciling cluster on the Reconciling cluster page first.
          </p>
        </Card>
      </div>
    )
  }

  return (
    <HyveConfigForm
      title="Settings"
      description={
        <>
          This organization's own <code className="rounded bg-neutral-100 px-1 dark:bg-neutral-800">HyveConfig</code>{' '}
          singleton, on its own dedicated reconciling cluster ({who?.reconcilingCluster}).
        </>
      }
      config={config}
      loading={loading}
      error={error}
      onSave={(form) => orgConfigApi.update(orgName, form)}
      onSaved={reload}
    />
  )
}
