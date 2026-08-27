import { apiFetch, apiFetchText } from './client'
import type { AuthContext } from './types'

export const kubeconfigApi = {
  /**
   * Fetches a minted kubeconfig for a cluster using module-auth/tunnel
   * access, or the API's own primary cluster. For the default (client-side
   * auth) method this 409s with a message pointing at authContextApi
   * instead — see internal/api/kubeconfig_handler.go's handleKubeconfig.
   */
  get: (clusterName: string) => apiFetchText(`/kubeconfig?cluster=${encodeURIComponent(clusterName)}`),
}

export const authContextApi = {
  /** Everything needed to run a driver module's auth op client-side — the console can't execute it (see AppShell's "run from your terminal" messaging), but can still surface driverSource/params/driverOutputs for inspection. */
  get: (clusterName: string) => apiFetch<AuthContext>(`/clusters/${encodeURIComponent(clusterName)}/auth-context`),
}
