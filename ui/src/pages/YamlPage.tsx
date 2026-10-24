// Any object as YAML: view, edit, or create. The plain way to manage what
// has no form of its own.
import { load } from 'js-yaml'
import { useState } from 'react'
import { Link, useNavigate, useParams, useSearchParams } from 'react-router-dom'
import { CodeEditor } from '../components/CodeEditor'
import { BackLink, Banner, Card, ConfirmButton, ErrorBanner, Loading, PageHeader } from '../components/ui'
import { toYaml } from '../lib/format'
import { useAction, useLoad } from '../lib/hooks'
import { can, newObject, remove, save, schemaFor, type Obj } from '../lib/kube'
import { detailPath, paths } from '../lib/routes'
import { DEFAULT_TEMPLATE_NAMESPACE } from '../lib/types'

export default function YamlPage() {
  const params = useParams()
  const type = params.type!
  const namespace = params.namespace === '_' ? undefined : params.namespace
  const name = params.name
  const creating = !name
  const [query] = useSearchParams()
  const editing = creating || query.get('mode') === 'edit'
  const navigate = useNavigate()
  const schema = schemaFor(type)
  const kind = schema?.kind || type
  const [text, setText] = useState('')
  const { busy, error, setError, run } = useAction()

  const { data: model, error: loadError, loading } = useLoad(
    async (r) => {
      const o: Obj = creating
        ? newObject(type, {
            metadata: { name: '', ...(schema?.namespaced ? { namespace: query.get('namespace') || DEFAULT_TEMPLATE_NAMESPACE } : {}) },
            spec: {},
          })
        : await r.find(type, namespace, name!)
      setText(toYaml(o, { dropStatus: editing }))
      return o
    },
    [type, namespace, name, editing],
    { live: !editing },
  )

  if (loading) return <Loading />
  if (!model) return <ErrorBanner error={loadError} />

  const listLink = <BackLink to={paths.list(type)} label={`${kind} list`} />
  const hasOwnPage = detailPath(type, model) !== paths.yaml(type, namespace, model.metadata.name)

  async function onSave() {
    let obj: Obj
    try {
      obj = load(text) as Obj
    } catch (e) {
      setError(`Not valid YAML: ${(e as Error).message}`)
      return
    }
    const saved = await run(() => save(type, { ...obj, status: obj.status ?? model!.status }))
    if (saved) navigate(paths.yaml(type, saved.metadata.namespace, saved.metadata.name))
  }

  return (
    <div>
      {hasOwnPage && !creating ? <BackLink to={detailPath(type, model)} label={model.metadata.name} /> : listLink}
      <PageHeader title={creating ? `New ${kind}` : `${kind} ${namespace ? `${namespace}/` : ''}${name}`}>
        {!editing && (
          <>
            {can(type, 'update') && (
              <Link className="btn btn-primary" to={paths.yaml(type, namespace, name!, true)}>
                Edit
              </Link>
            )}
            {can(type, 'delete') && (
              <ConfirmButton
                label="Delete"
                confirm={`Delete ${name}?`}
                onConfirm={() =>
                  run(async () => {
                    await remove(type, model)
                    navigate(paths.list(type))
                  })
                }
              />
            )}
          </>
        )}
      </PageHeader>
      <ErrorBanner error={error || loadError} />
      {model.metadata.deletionTimestamp && <Banner tone="warning">Being deleted.</Banner>}
      <Card>
        <CodeEditor value={text} onChange={setText} readOnly={!editing} minHeight="28rem" upload={editing} />
        {editing && (
          <div className="mt-4 flex justify-end gap-2">
            <button
              type="button"
              className="btn btn-tertiary"
              onClick={() => navigate(creating ? paths.list(type) : paths.yaml(type, namespace, name!))}
            >
              Cancel
            </button>
            <button type="button" className="btn btn-primary" disabled={busy} onClick={onSave}>
              {creating ? 'Create' : 'Save'}
            </button>
          </div>
        )}
      </Card>
    </div>
  )
}
