// A ClusterTemplate: view, edit or create it, preview what a cluster from
// it gets, or make one; and the Secrets its resources copy to clusters.
import { useState } from 'react'
import { Link, useNavigate, useParams, useSearchParams } from 'react-router-dom'
import { RenderDialog } from '../components/RenderDialog'
import { SecretsSection } from '../components/SecretsSection'
import { TemplateForm, type Mode } from '../components/TemplateForm'
import { BackLink, Banner, Card, ErrorBanner, Loading, PageHeader } from '../components/ui'
import { useLoad } from '../lib/hooks'
import { can, newObject } from '../lib/kube'
import { paths } from '../lib/routes'
import { templateSecrets } from '../lib/secrets'
import { DEFAULT_TEMPLATE_NAMESPACE, TYPES } from '../lib/types'

export default function TemplatePage() {
  const { namespace, name } = useParams()
  const [query] = useSearchParams()
  const navigate = useNavigate()
  const mode: Mode = !name ? 'create' : query.get('mode') === 'edit' ? 'edit' : 'view'
  const [previewing, setPreviewing] = useState(false)

  const { data: value, error, loading } = useLoad(
    async (r) =>
      mode === 'create'
        ? newObject(TYPES.TEMPLATE, { metadata: { name: '', namespace: query.get('namespace') || DEFAULT_TEMPLATE_NAMESPACE }, spec: {} })
        : r.find(TYPES.TEMPLATE, namespace, name!),
    [mode, namespace, name],
    { live: mode === 'view' },
  )

  if (loading) return <Loading />
  if (!value) return <ErrorBanner error={error} />
  const ns = value.metadata.namespace!

  return (
    <div>
      <BackLink to={paths.templates} label="Templates" />
      <PageHeader title={mode === 'create' ? 'New template' : value.metadata.name}>
        {mode === 'view' && (
          <>
            <button type="button" className="btn btn-secondary" onClick={() => setPreviewing(true)}>
              Preview
            </button>
            <Link className="btn btn-secondary" to={paths.yaml(TYPES.TEMPLATE, ns, value.metadata.name)}>
              YAML
            </Link>
            {can(TYPES.TEMPLATE, 'update') && (
              <Link className="btn btn-secondary" to={paths.template(ns, value.metadata.name, true)}>
                Edit
              </Link>
            )}
            <button type="button" className="btn btn-primary" onClick={() => navigate(paths.createCluster({ namespace: ns, name: value.metadata.name }))}>
              New cluster
            </button>
          </>
        )}
      </PageHeader>
      {value.metadata.deletionTimestamp && <Banner tone="warning">Being deleted.</Banner>}
      {previewing && <RenderDialog template={value} onClose={() => setPreviewing(false)} />}
      <Card>
        <TemplateForm
          key={mode === 'view' ? value.metadata.resourceVersion : mode}
          value={value}
          mode={mode}
          onSaved={(o) => navigate(paths.template(o.metadata.namespace!, o.metadata.name))}
          onCancel={() => navigate(mode === 'create' ? paths.templates : paths.template(namespace!, name!))}
        />
      </Card>
      {mode === 'view' && (
        <Card title="Secrets" className="mt-4">
          <p className="text-muted mt-0 text-sm">
            What this template's resources copy to every cluster made from it, from {ns}. Clusters get a change the next time Fleet copies the Secret.
          </p>
          <SecretsSection
            load={(r) => templateSecrets(r, value)}
            deps={[ns, value.metadata.name, value.metadata.resourceVersion]}
            hint={`In ${ns}, the template namespace: Fleet copies it to every cluster made from ${value.metadata.name} whose resources name it.`}
            empty="None of this template's resources copy a Secret."
          />
        </Card>
      )}
    </div>
  )
}
