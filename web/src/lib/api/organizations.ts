import { apiFetch } from './client'
import type { Organization, OrganizationEnvironment } from './types'

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
  // field above.
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
}
