// A shared Resource: one Fleet spec that templates and clusters refer to
// by name, with the same form as their own resources ('hyve resource
// create NAME --helm/--git/--path').
import { useRef, useState } from 'react'
import { Link, useNavigate, useParams, useSearchParams } from 'react-router-dom'
import { NameNamespace } from '../components/NameNamespace'
import { ResourceForm, type ResourceFormHandle } from '../components/ResourceForm'
import { BackLink, Banner, Card, ErrorBanner, Field, Loading, PageHeader } from '../components/ui'
import { ViewDialog } from '../components/ViewDialog'
import { useLoad } from '../lib/hooks'
import { can, newObject, save, type Obj } from '../lib/kube'
import { describe, kindOf } from '../lib/resources'
import { paths } from '../lib/routes'
import { DEFAULT_TEMPLATE_NAMESPACE, TYPES } from '../lib/types'

type Mode = 'create' | 'edit' | 'view'

export default function SharedResourcePage() {
  const { namespace, name } = useParams()
  const [query] = useSearchParams()
  const navigate = useNavigate()
  const mode: Mode = !name ? 'create' : query.get('mode') === 'edit' ? 'edit' : 'view'

  const { data: value, error, loading } = useLoad(
    async (r) =>
      mode === 'create'
        ? newObject(TYPES.RESOURCE, { metadata: { name: '', namespace: query.get('namespace') || DEFAULT_TEMPLATE_NAMESPACE }, spec: {} })
        : r.find(TYPES.RESOURCE, namespace, name!),
    [mode, namespace, name],
    { live: mode === 'view' },
  )

  if (loading) return <Loading />
  if (!value) return <ErrorBanner error={error} />
  const ns = value.metadata.namespace!

  return (
    <div>
      <BackLink to={paths.resources} label="Resources" />
      <PageHeader title={mode === 'create' ? 'New resource' : value.metadata.name}>
        {mode !== 'create' && (
          <Link className="btn btn-secondary" to={paths.yaml(TYPES.RESOURCE, ns, value.metadata.name)}>
            YAML
          </Link>
        )}
        {mode === 'view' && can(TYPES.RESOURCE, 'update') && (
          <Link className="btn btn-primary" to={paths.resource(ns, value.metadata.name, true)}>
            Edit
          </Link>
        )}
      </PageHeader>
      {value.metadata.deletionTimestamp && <Banner tone="warning">Being deleted.</Banner>}
      <Card>
        <SharedResourceForm
          key={mode === 'view' ? value.metadata.resourceVersion : mode}
          value={value}
          mode={mode}
          onSaved={(o) => navigate(paths.resource(o.metadata.namespace!, o.metadata.name))}
          onCancel={() => navigate(mode === 'create' ? paths.resources : paths.resource(namespace!, name!))}
        />
      </Card>
    </div>
  )
}

function SharedResourceForm({ value, mode, onSaved, onCancel }: { value: Obj; mode: Mode; onSaved: (o: Obj) => void; onCancel: () => void }) {
  const isView = mode === 'view'
  const [name, setName] = useState(value.metadata.name || '')
  const [namespace, setNamespace] = useState(value.metadata.namespace || '')
  const [viewing, setViewing] = useState(false)
  const [errors, setErrors] = useState<string[]>([])
  const [busy, setBusy] = useState(false)
  const rform = useRef<ResourceFormHandle>(null)
  const spec = value.spec || {}
  const entry = Object.keys(spec).length ? { ...spec, name: value.metadata.name } : null

  async function onSave() {
    setErrors([])
    setBusy(true)
    try {
      const e = rform.current!.entry()
      delete e.name
      const o: Obj = JSON.parse(JSON.stringify(value))
      o.metadata.name = name
      o.metadata.namespace = namespace
      o.spec = e
      onSaved(await save(TYPES.RESOURCE, o))
    } catch (err) {
      setErrors([(err as Error)?.message || String(err)])
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="space-y-4">
      <NameNamespace name={name} namespace={namespace} onName={setName} onNamespace={setNamespace} fixed={mode !== 'create'} />
      <Banner>Templates and clusters refer to this by name, and can override its Helm values. Changes reach every cluster that has it.</Banner>
      {isView ? (
        <div>
          <Field label="Kind">{entry ? kindOf(entry) : '-'}</Field>
          <Field label="Source">
            <span className="break-all">{entry ? describe(entry) : '-'}</span>
          </Field>
          <Field label="Target namespace">{spec.namespace || '-'}</Field>
          <Field label="Secrets to copy">{(spec.secrets || []).join(', ') || '-'}</Field>
          <button type="button" className="btn btn-tertiary btn-sm mt-2" onClick={() => setViewing(true)}>
            View
          </button>
        </div>
      ) : (
        <ResourceForm ref={rform} value={entry} allowRef={false} noName />
      )}
      {errors.map((e, i) => (
        <Banner key={i} tone="error">
          {e}
        </Banner>
      ))}
      {!isView && (
        <div className="flex justify-end gap-2 border-t border-neutral-100 pt-4 dark:border-neutral-800">
          <button type="button" className="btn btn-tertiary" onClick={onCancel}>
            Cancel
          </button>
          <button type="button" className="btn btn-primary" disabled={busy || !name} onClick={onSave}>
            {mode === 'create' ? 'Create' : 'Save'}
          </button>
        </div>
      )}
      {viewing && <ViewDialog title={value.metadata.name} sections={[{ label: 'Resource', object: value }]} onClose={() => setViewing(false)} />}
    </div>
  )
}
