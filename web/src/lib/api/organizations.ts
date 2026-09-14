import { apiDelete, apiFetch } from './client'
import type { Organization, OrganizationEnvironment, OrgReconcilingClusterStatus } from './types'

export const organizationsApi = {
  list: () => apiFetch<Organization[]>('/organizations'),
  create: (name: string) => apiFetch<Organization>('/organizations', { method: 'POST', body: JSON.stringify({ name }) }),
  listEnvironments: (name: string) =>
    apiFetch<OrganizationEnvironment[]>(`/organizations/${encodeURIComponent(name)}/environments`),
  createEnvironment: (name: string, environment: string) =>
    apiFetch<OrganizationEnvironment>(`/organizations/${encodeURIComponent(name)}/environments`, {
      method: 'POST',
      body: JSON.stringify({ name: environment }),
    }),
  // GET/PUT/DELETE /organizations/{name}/reconciling-cluster — an
  // organization's own admin-facing self-service placement (register/edit,
  // or permanently remove, its own dedicated cluster), reachable by an
  // ordinary admin for their own organization, not just a superadmin.
  getOwnReconcilingCluster: (name: string) =>
    apiFetch<OrgReconcilingClusterStatus>(`/organizations/${encodeURIComponent(name)}/reconciling-cluster`),
  // kubeconfig: "" moves the organization back onto the control plane's
  // own home cluster.
  setOwnReconcilingCluster: (name: string, kubeconfig: string) =>
    apiFetch<Organization>(`/organizations/${encodeURIComponent(name)}/reconciling-cluster`, {
      method: 'PUT',
      body: JSON.stringify({ kubeconfig }),
    }),
  deleteOwnReconcilingCluster: (name: string) => apiDelete(`/organizations/${encodeURIComponent(name)}/reconciling-cluster`),
}
