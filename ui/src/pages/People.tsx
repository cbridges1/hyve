// Users and Access, as two tabs of one page (one sidebar item): the
// accounts, and what they and their groups may reach. Users is shown when
// the host has its own (hyve.io User), Access always.
import { Outlet, useLocation } from 'react-router-dom'
import { PageHeader, Tabs } from '../components/ui'
import { schemaFor } from '../lib/kube'
import { paths } from '../lib/routes'
import { TYPES } from '../lib/types'

export default function People() {
  const { pathname } = useLocation()
  const tabs = [
    ...(schemaFor(TYPES.USER) ? [{ label: 'Users', to: paths.users, active: pathname === paths.users }] : []),
    { label: 'Access', to: paths.access, active: pathname === paths.access },
  ]
  return (
    <div>
      <PageHeader title="People" />
      <Tabs tabs={tabs} />
      <Outlet />
    </div>
  )
}
