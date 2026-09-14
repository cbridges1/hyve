import { Card } from '../components/Card'
import { EnvironmentsSection } from '../components/EnvironmentsManager'
import { useWhoami } from '../lib/useWhoami'

// Unlike OrganizationsPage (superadmin-only, lists every tenant on the
// install and can manage any of them by name), this page is reachable by
// an ordinary admin too, scoped implicitly to their own organization via
// whoami's own namespace — the same organizationsApi.listEnvironments/
// createEnvironment calls as OrganizationsPage's own row, just resolved
// from "who am I" instead of a name picked from a cross-tenant list.
// org.Name and org.Namespace are always the same value by construction
// (see internal/api's handleCreateOrganization), so who.namespace doubles
// as the organization name every organizationsApi call expects.
//
// A superadmin viewing the control plane (no organization selected, or —
// pre this milestone's own full-merge follow-up — the control plane
// having no Organization row of its own at all) sees "no organization
// selected" here rather than an error: this page has nothing to manage
// until there's a real organization backing the current view.
export function EnvironmentsPage() {
  const { data: who, loading } = useWhoami()

  return (
    <div className="space-y-4">
      <div>
        <h1 className="text-lg font-semibold text-neutral-900 dark:text-neutral-100">Environments</h1>
        <p className="mt-0.5 text-sm text-neutral-500">
          Named sub-scopes within your own organization — pass <code>?env=</code> when creating a cluster/template/
          workflow/resource to target one.
        </p>
      </div>

      {loading && <p className="text-sm text-neutral-500">Loading…</p>}

      {who && (
        <Card>
          {who.reconcilingCluster && (
            <div className="mb-4">
              <div className="mb-1 text-xs font-medium tracking-wide text-neutral-500 uppercase dark:text-neutral-500">
                Reconciling cluster
              </div>
              <p className="text-sm text-neutral-800 dark:text-neutral-200">
                {who.reconcilingCluster}
                {who.migrating && (
                  <span className="ml-2 rounded-full border border-amber-300 px-2 py-0.5 text-xs text-amber-700 dark:border-amber-700 dark:text-amber-400">
                    migrating
                  </span>
                )}
              </p>
              <p className="mt-0.5 text-xs text-neutral-500">Only a superadmin can move an organization to a different cluster.</p>
            </div>
          )}
          <EnvironmentsSection orgName={who.namespace} />
        </Card>
      )}
    </div>
  )
}
