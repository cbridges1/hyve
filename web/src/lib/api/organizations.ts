import { apiDelete, apiFetch } from './client'
import type { Organization, OrganizationEnvironment, OrgReconcilingCluster, OrgReconcilingClusterStatus } from './types'

const orgPath = (name: string, rest = '') => `/organizations/${encodeURIComponent(name)}${rest}`

export const organizationsApi = {
  list: () => apiFetch<Organization[]>('/organizations'),
  create: (name: string) => apiFetch<Organization>('/organizations', { method: 'POST', body: JSON.stringify({ name }) }),
  // Renames the organization's own display name — its Namespace (the real
  // Kubernetes namespace) never changes. Must stay unique; subject to the
  // same reserved-name rules as create.
  rename: (name: string, newName: string) =>
    apiFetch<Organization>(`/organizations/${encodeURIComponent(name)}`, {
      method: 'PATCH',
      body: JSON.stringify({ name: newName }),
    }),
  // Marks the organization for deletion and issues the namespace delete —
  // permanent, and asynchronous (202, no body — see that endpoint's own
  // doc comment): the row itself disappears once the namespace finishes
  // terminating, not synchronously with this call.
  delete: (name: string) => apiDelete(`/organizations/${encodeURIComponent(name)}`),
  listEnvironments: (name: string) =>
    apiFetch<OrganizationEnvironment[]>(`/organizations/${encodeURIComponent(name)}/environments`),
  createEnvironment: (name: string, environment: string) =>
    apiFetch<OrganizationEnvironment>(`/organizations/${encodeURIComponent(name)}/environments`, {
      method: 'POST',
      body: JSON.stringify({ name: environment }),
    }),
  // Permanently removes one environment. Refused (409) if it still has any
  // clusters/templates/workflows/resources, or (400) if it's the
  // organization's last remaining environment — see that endpoint's own
  // doc comment.
  deleteEnvironment: (name: string, environment: string) =>
    apiDelete(`/organizations/${encodeURIComponent(name)}/environments/${encodeURIComponent(environment)}`),
  // An organization's own reconciling clusters — reachable by an ordinary
  // admin for their own organization, not just a superadmin. It can keep
  // several stored (add/remove) and switch between them by name (use)
  // without re-entering a kubeconfig.
  getOwnReconcilingCluster: (name: string) => apiFetch<OrgReconcilingClusterStatus>(orgPath(name, '/reconciling-cluster')),
  listReconcilingClusters: (name: string) => apiFetch<OrgReconcilingCluster[]>(orgPath(name, '/reconciling-clusters')),
  // Stores (or, for an existing name, rotates the kubeconfig of) one of
  // the organization's own clusters without switching to it.
  addReconcilingCluster: (name: string, cluster: string, kubeconfig: string) =>
    apiFetch<OrgReconcilingCluster>(orgPath(name, '/reconciling-clusters'), {
      method: 'POST',
      body: JSON.stringify({ name: cluster, kubeconfig }),
    }),
  // Refused (409) for the active cluster — switch elsewhere first.
  removeReconcilingCluster: (name: string, cluster: string) =>
    apiDelete(orgPath(name, `/reconciling-clusters/${encodeURIComponent(cluster)}`)),
  // Switches onto one of the organization's own named clusters, or back to
  // the home cluster for null. Copies every
  // resource first, so this resolves only once that's done.
  useReconcilingCluster: (name: string, cluster: string | null) =>
    apiFetch<OrgReconcilingClusterStatus>(orgPath(name, '/reconciling-cluster'), {
      method: 'PUT',
      body: JSON.stringify(cluster ? { name: cluster } : {}),
    }),
}
