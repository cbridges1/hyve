import { RoleSuperadmin } from '../lib/api/auth'
import { useActAs } from '../lib/useActAs'
import { useWhoami } from '../lib/useWhoami'
import { OrgReconcilingClusterPage } from './OrgReconcilingClusterPage'
import { ReconcilingClustersPage } from './ReconcilingClustersPage'

// Picks between the two reconciling-cluster pages the same way AppShell's
// own sidebar link does (see that component's own doc comment): the
// superadmin-only shared registry (control plane view only), or an
// organization's own self-service placement otherwise.
export function ReconcilingClusterRoute() {
  const who = useWhoami().data
  const [actAs] = useActAs()
  if (who?.role === RoleSuperadmin && actAs === null) return <ReconcilingClustersPage />
  return <OrgReconcilingClusterPage />
}
