import { apiDelete, apiFetch } from './client'
import type { Organization, OrganizationEnvironment, OrgReconcilingClusterStatus } from './types'

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
