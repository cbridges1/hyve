// The management cluster in the explorer: CAPI, Fleet and hyve's own
// objects, k3s's add-ons, anything else it runs. Through the host's
// /k8s/clusters/local as the signed-in user, so its RBAC decides what's
// shown, as on a child cluster.
import { lazy, Suspense } from 'react'
import { Loading, PageHeader } from '../components/ui'
import { LOCAL_CLUSTER } from '../lib/explorer'
import { openShell } from '../lib/kubectlShell'

const Explorer = lazy(() => import('../components/explorer/Explorer').then((m) => ({ default: m.Explorer })))

export default function ManagementExplorer() {
  return (
    <div className="space-y-4">
      <PageHeader title="Explorer" subtitle="Every resource type the management cluster serves, CRDs included.">
        <button
          type="button"
          className="btn btn-secondary"
          title="kubectl, helm and k9s in your browser, as you, on the management cluster"
          onClick={() => openShell({ cluster: LOCAL_CLUSTER, label: 'management cluster' })}
        >
          Kubectl shell
        </button>
      </PageHeader>
      <Suspense fallback={<Loading />}>
        <Explorer clusterId={LOCAL_CLUSTER} />
      </Suspense>
    </div>
  )
}
