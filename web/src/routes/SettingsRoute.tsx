import { RoleSuperadmin } from '../lib/api/auth'
import { useActAs } from '../lib/useActAs'
import { useWhoami } from '../lib/useWhoami'
import { OrgSettingsPage } from './OrgSettingsPage'
import { SettingsPage } from './SettingsPage'

// Picks between the two settings pages the same way AppShell's own sidebar
// link does (see that component's own doc comment): the control plane's
// own home-cluster HyveConfig singleton (control plane view only), or an
// organization's own dedicated-reconciling-cluster HyveConfig otherwise.
export function SettingsRoute() {
  const who = useWhoami().data
  const [actAs] = useActAs()
  if (who?.role === RoleSuperadmin && actAs === null) return <SettingsPage />
  return <OrgSettingsPage />
}
