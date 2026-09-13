import { apiFetch } from './client'
import type { ReconcilingCluster } from './types'

export const reconcilingClustersApi = {
  list: () => apiFetch<ReconcilingCluster[]>('/reconciling-clusters'),
  // Idempotent by name on the server (internal/api's own
  // handleCreateReconcilingCluster) — re-registering an existing name
  // rotates its kubeconfig rather than erroring.
  create: (name: string, kubeconfig: string) =>
    apiFetch<ReconcilingCluster>('/reconciling-clusters', {
      method: 'POST',
      body: JSON.stringify({ name, kubeconfig }),
    }),
}
