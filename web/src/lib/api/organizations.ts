import { apiFetch } from './client'
import type { Organization, OrganizationEnvironment, OrgReconcilingClusterStatus } from './types'

export const organizationsApi = {
  list: () => apiFetch<Organization[]>('/organizations'),
  create: (name: string, reconcilingCluster?: string) =>
    apiFetch<Organization>('/organizations', {
      method: 'POST',
      body: JSON.stringify(reconcilingCluster ? { name, reconcilingCluster } : { name }),
    }),
  // reconcilingCluster: "" moves the organization back to the control
  // plane's own home cluster — see internal/api's patchOrganizationRequest
  // for why this is always sent (never omitted), unlike create's optional
  // field above. Superadmin-only, picks from the shared registry
  // (reconcilingClustersApi.list) — for an organization's own self-service
  // dedicated cluster, see getOwnReconcilingCluster/setOwnReconcilingCluster
  // below instead.
  setReconcilingCluster: (name: string, reconcilingCluster: string) =>
    apiFetch<Organization>(`/organizations/${encodeURIComponent(name)}`, {
      method: 'PATCH',
      body: JSON.stringify({ reconcilingCluster }),
    }),
  listEnvironments: (name: string) =>
    apiFetch<OrganizationEnvironment[]>(`/organizations/${encodeURIComponent(name)}/environments`),
  createEnvironment: (name: string, environment: string) =>
    apiFetch<OrganizationEnvironment>(`/organizations/${encodeURIComponent(name)}/environments`, {
      method: 'POST',
      body: JSON.stringify({ name: environment }),
    }),
  // GET/PUT /organizations/{name}/reconciling-cluster — an organization's
  // own admin-facing self-service placement, reachable by an ordinary
  // admin for their own organization (not just a superadmin), unlike
  // setReconcilingCluster above.
  getOwnReconcilingCluster: (name: string) =>
    apiFetch<OrgReconcilingClusterStatus>(`/organizations/${encodeURIComponent(name)}/reconciling-cluster`),
  // kubeconfig: "" moves the organization back onto the control plane's
  // own home cluster — mirrors setReconcilingCluster's own "" convention.
  setOwnReconcilingCluster: (name: string, kubeconfig: string) =>
    apiFetch<Organization>(`/organizations/${encodeURIComponent(name)}/reconciling-cluster`, {
      method: 'PUT',
      body: JSON.stringify({ kubeconfig }),
    }),
}
