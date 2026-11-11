// New Deployment, or edit one: a source (Helm chart, git paths or
// manifests: the resource form), where it goes (the target picker), how
// an update rolls out, and whether it's paused. Saved as one Fleet HelmOp,
// GitRepo or Bundle (lib/delivery.ts); fields the form doesn't model are
// kept, and "Edit as YAML" edits the Fleet object itself.
// ?to=template/civo (repeatable) starts a new one aimed there.
import { useRef, useState } from 'react'
import { Link, useNavigate, useParams, useSearchParams } from 'react-router-dom'
import { Checkbox, SelectField, TextField } from '../components/form'
import { ResourceForm, type ResourceFormHandle } from '../components/ResourceForm'
import { TargetPicker } from '../components/TargetPicker'
import { BackLink, Banner, Card, ErrorBanner, Loading, PageHeader } from '../components/ui'
import {
  deploymentObject,
  entryOf,
  HOST_WORKSPACE,
  loadDelivery,
  parseTarget,
  ROLLOUTS,
  type Rollout,
  type Target,
  type TargetKind,
} from '../lib/delivery'
import { useAction, useLoad } from '../lib/hooks'
import { kube, newObject, save, type Obj } from '../lib/kube'
import { paths } from '../lib/routes'
import { DEFAULT_TEMPLATE_NAMESPACE } from '../lib/types'

const NAME_RE = /^[a-z0-9]([-a-z0-9]*[a-z0-9])?$/

function b64(s: string) {
  return btoa(String.fromCharCode(...new TextEncoder().encode(s)))
}

export default function DeploymentEdit() {
  const { namespace, name } = useParams()
  const [query] = useSearchParams()
  const navigate = useNavigate()
  const editing = !!name
  const { data, error, loading } = useLoad(
    async (r) => {
      const dl = await loadDelivery(r)
      const d = editing ? dl.deployments.find((x) => x.namespace === namespace && x.name === name) : undefined
      if (editing && !d) throw new Error(`No Deployment ${namespace}/${name}.`)
      return { dl, d }
    },
    [namespace, name],
    { live: false },
  )
  if (loading) return <Loading />
  if (!data) return <ErrorBanner error={error} />
  return <Form key={`${namespace}/${name}`} dl={data.dl} existing={data.d?.model} initialTargets={data.d?.targets || (query.getAll('to').map(parseTarget).filter(Boolean) as Target[])} rollout={data.d?.rollout || 'all'} paused={!!data.d?.paused} navigate={navigate} />
}

function Form({
  dl,
  existing,
  initialTargets,
  rollout: startRollout,
  paused: startPaused,
  navigate,
}: {
  dl: Awaited<ReturnType<typeof loadDelivery>>
  existing?: Obj
  initialTargets: Target[]
  rollout: Rollout
  paused: boolean
  navigate: (to: string) => void
}) {
  const [name, setName] = useState(existing?.metadata.name || '')
  const [targets, setTargets] = useState<Target[]>(initialTargets)
  const [rollout, setRollout] = useState<Rollout>(startRollout)
  const [paused, setPaused] = useState(startPaused)
  const [username, setUsername] = useState('')
  const [token, setToken] = useState('')
  const formRef = useRef<ResourceFormHandle>(null)
  const action = useAction()
  const ns = existing?.metadata.namespace
  // An environment's Deployment reaches only that environment's clusters;
  // fleet-local's only the host.
  const envHome = ns && ns !== DEFAULT_TEMPLATE_NAMESPACE && ns !== HOST_WORKSPACE ? ns : ''
  const only: TargetKind[] | undefined = ns === HOST_WORKSPACE ? ['host'] : envHome ? ['cluster', 'env'] : undefined
  const options = {
    templates: dl.templates,
    classes: dl.classes,
    environments: envHome ? [envHome] : dl.environments,
    clusters: envHome ? dl.clusters.filter((c) => c.metadata.namespace === envHome) : dl.clusters,
    groups: dl.groups.filter((g) => g.metadata.namespace === DEFAULT_TEMPLATE_NAMESPACE),
  }
  const isGit = existing?.kind === 'GitRepo'

  const submit = () =>
    action.run(async () => {
      if (!existing && (!NAME_RE.test(name) || name.length > 40)) throw new Error('Name: lowercase letters, digits and dashes, at most 40.')
      const entry = formRef.current!.entry()
      let obj = deploymentObject({ name, entry, targets, rollout, paused }, existing)
      const type = obj.kind === 'GitRepo' ? 'fleet.cattle.io.gitrepo' : obj.kind === 'HelmOp' ? 'fleet.cattle.io.helmop' : 'fleet.cattle.io.bundle'
      if (token) {
        if (!entry.gitRepo) throw new Error('A git token is for a git source.')
        if (!username) throw new Error('A token needs a username.')
        // The same Secret 'hyve deploy create --username --token-env' makes.
        const secretName = `${name}-git-auth`
        const sns = obj.metadata.namespace!
        const secret =
          (await kube.maybe('secret', sns, secretName)) ||
          newObject('secret', { metadata: { name: secretName, namespace: sns }, type: 'kubernetes.io/basic-auth' })
        secret.data = { username: b64(username), password: b64(token) }
        await save('secret', secret)
        obj.spec.clientSecretName = secretName
      } else if (existing?.spec?.clientSecretName && entry.gitRepo) {
        obj.spec.clientSecretName = existing.spec.clientSecretName
      }
      if (!existing) obj = newObject(type, obj as Obj & { metadata: Obj['metadata'] })
      const saved = await save(type, obj)
      navigate(paths.deployment(saved.metadata.namespace!, saved.metadata.name))
    })

  return (
    <div>
      <BackLink to={existing ? paths.deployment(ns!, existing.metadata.name) : paths.fleet} label={existing ? existing.metadata.name : 'Fleet'} />
      <PageHeader title={existing ? `Edit ${existing.metadata.name}` : 'New deployment'}>
        {existing && (
          <Link className="btn btn-secondary" to={paths.yaml(existing.kind === 'GitRepo' ? 'fleet.cattle.io.gitrepo' : existing.kind === 'HelmOp' ? 'fleet.cattle.io.helmop' : 'fleet.cattle.io.bundle', ns, existing.metadata.name, true)}>
            Edit as YAML
          </Link>
        )}
      </PageHeader>
      <Card title="What">
        {!existing && <TextField className="mb-3 sm:w-1/3" label="Name" required value={name} onChange={setName} hint="Its Fleet object's name, and a Helm chart's release name." />}
        <ResourceForm ref={formRef} value={existing ? entryOf(existing) : null} allowRef={false} noName />
        <details className="mt-3">
          <summary className="cursor-pointer text-sm">Private git repository</summary>
          <div className="mt-2 grid gap-3 sm:grid-cols-2">
            <TextField label="Git username" autoComplete="off" value={username} onChange={setUsername} />
            <TextField
              label="Git token"
              type="password"
              autoComplete="new-password"
              value={token}
              onChange={setToken}
              placeholder={existing?.spec?.clientSecretName ? 'leave empty to keep the saved one' : ''}
            />
          </div>
          <p className="text-muted text-sm">Kept in a basic-auth Secret (NAME-git-auth) beside the Deployment, which Fleet uses to clone.</p>
        </details>
      </Card>
      <Card title="Where" className="mt-4">
        {envHome && <Banner>This Deployment lives in environment {envHome}, so it reaches that environment's clusters only.</Banner>}
        <TargetPicker value={targets} onChange={setTargets} options={options} only={only} />
        <p className="text-muted mt-2 text-sm">
          Every cluster a target names gets it, now and later. A template's, class's or environment's clusters are Fleet ClusterGroups hyve keeps up to date.
        </p>
      </Card>
      <Card title="How" className="mt-4">
        <div className="grid items-end gap-3 sm:grid-cols-2">
          <SelectField
            label="Roll out changes"
            value={rollout}
            onChange={(v) => setRollout(v as Rollout)}
            disabled={isGit}
            options={[...ROLLOUTS, ...(rollout === 'custom' ? [{ value: 'custom' as Rollout, label: 'Custom (kept from its YAML)' }] : [])]}
            hint={isGit ? "A git source's rollout is set per bundle in its fleet.yaml." : rollout === 'canary' ? 'Clusters labelled hyve.io/canary=true update first; the rest once they are ready.' : undefined}
          />
          <div className="pb-2">
            <Checkbox checked={paused} onChange={setPaused} label="Paused: changes don't reach clusters until resumed" />
          </div>
        </div>
      </Card>
      <ErrorBanner error={action.error} />
      <div className="mt-4 flex gap-2.5">
        <button type="button" className="btn btn-primary" disabled={action.busy} onClick={submit}>
          {existing ? 'Save' : 'Create'}
        </button>
        <button type="button" className="btn btn-secondary" onClick={() => navigate(existing ? paths.deployment(ns!, existing.metadata.name) : paths.fleet)}>
          Cancel
        </button>
      </div>
    </div>
  )
}
