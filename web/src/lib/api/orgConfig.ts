import { apiFetch } from './client'
import type { HyveConfig } from './types'

// GET/PATCH /organizations/{name}/config (internal/api/orgconfig.go) — an
// organization's own admin-facing HyveConfig, scoped to that organization's
// own namespace on its own dedicated reconciling cluster. Distinct from
// configApi (lib/api/config.ts), which always targets the control plane's
// own home-cluster singleton and stays superadmin/control-plane-only.
export const orgConfigApi = {
  get: (name: string) => apiFetch<HyveConfig>(`/organizations/${encodeURIComponent(name)}/config`),
  update: (name: string, config: Omit<HyveConfig, 'exists'>) =>
    apiFetch<HyveConfig>(`/organizations/${encodeURIComponent(name)}/config`, {
      method: 'PATCH',
      body: JSON.stringify(config),
    }),
}
