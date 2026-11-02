// Every Fleet GitRepo people add (not the ones hyve makes for template
// resources) and their bundles' state: components/RepoManager.tsx. Those
// in fleet-local deploy to the host cluster (local); other workspaces
// target their clusters.
import { RepoManager } from '../components/RepoManager'
import { PageHeader } from '../components/ui'

export default function Repos() {
  return (
    <RepoManager
      header={(add) => (
        <PageHeader
          title="Repos"
          subtitle="Fleet GitRepos. In fleet-local they deploy to the host cluster, local (ClusterClasses, templates); other workspaces target their clusters."
        >
          {add}
        </PageHeader>
      )}
    />
  )
}
