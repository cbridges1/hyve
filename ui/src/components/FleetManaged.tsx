// A warning on an object Fleet deployed (it marks them with its Helm
// release, named after the bundle, and an objectset id): an edit made here
// lasts only until Fleet next deploys the bundle, and at once if its repo
// undoes changes. Where the bundle comes from a git repo, links to it.
import { Link } from 'react-router-dom'
import { useLoad } from '../lib/hooks'
import type { Obj } from '../lib/kube'
import { isUserRepo, REPO_LABEL, shortBundleName } from '../lib/repos'
import { FLEET } from '../lib/resources'
import { paths } from '../lib/routes'
import { LABELS } from '../lib/types'
import { Banner } from './ui'

export function FleetManaged({ obj }: { obj: Obj }) {
  const a = obj.metadata.annotations || {}
  const release = a['meta.helm.sh/release-name']
  const managed = !!release && !!a['objectset.rio.cattle.io/id']
  // Bundles live on the management cluster, whichever cluster the object is on.
  const { data } = useLoad(
    async (r) => {
      if (!managed) return null
      const bundle = (await r.findAll(FLEET.BUNDLE).catch(() => [] as Obj[])).find((b) => b.metadata.name === release)
      const repoName = bundle?.metadata.labels?.[REPO_LABEL]
      const repo = repoName ? await r.maybe(FLEET.GIT_REPO, bundle!.metadata.namespace, repoName).catch(() => null) : null
      return { bundle, repo }
    },
    [managed, release],
  )
  // Not until the lookup's done, so the text doesn't change under the reader.
  if (!managed || data === undefined) return null

  const bundle = data?.bundle
  const repo = data?.repo
  const until = repo?.spec?.correctDrift?.enabled
    ? 'Fleet undoes changes made here right away: the repo has "Undo changes" on.'
    : 'A change made here lasts until Fleet next deploys the bundle (a commit that changes it, or Redeploy).'

  if (repo && bundle && isUserRepo(repo)) {
    return (
      <Banner tone="warning">
        Fleet deployed this from git: bundle <strong>{shortBundleName(repo, bundle)}</strong> of repo{' '}
        <Link to={paths.repo(repo.metadata.namespace!, repo.metadata.name)}>{repo.metadata.name}</Link>. {until} Change it in git instead.
      </Banner>
    )
  }
  const owner = bundle?.metadata.labels?.[LABELS.OWNER]
  const ownerKind = bundle?.metadata.labels?.[LABELS.OWNER_KIND]
  return (
    <Banner tone="warning">
      Fleet deployed this (bundle {release}
      {owner ? `, for hyve's ${ownerKind === 'ClusterTemplate' ? 'template' : 'cluster resource'} ${owner}` : ''}). A change made here lasts
      until Fleet next deploys it; change it where it's defined instead.
    </Banner>
  )
}
