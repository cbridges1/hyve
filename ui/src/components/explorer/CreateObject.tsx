// The explorer's Create: a Secret in the key/value form (SecretEdit),
// anything else as YAML from a starting point. As the signed-in user, on
// the cluster being explored.
import { useMemo, useState } from 'react'
import { errorMessage } from '../../lib/api'
import { createFromYaml, skeletonYaml } from '../../lib/explorer'
import type { Schema } from '../../lib/kube'
import { clusterSecretApi } from '../../lib/secrets'
import { CodeEditor } from '../CodeEditor'
import { Modal } from '../Modal'
import { SecretEdit } from '../SecretEdit'
import { ErrorBanner } from '../ui'

export function CreateObject({
  clusterId,
  schema,
  namespace,
  namespaces,
  onClose,
  onCreated,
}: {
  clusterId: string
  schema: Schema
  // The list's namespace, '' for all of them.
  namespace: string
  namespaces: string[]
  onClose: () => void
  onCreated: (name: string, namespace?: string) => void
}) {
  const api = useMemo(() => clusterSecretApi(clusterId, schema), [clusterId, schema])
  const [text, setText] = useState(() => skeletonYaml(schema, namespace))
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  if (schema.id === 'secret')
    return (
      <SecretEdit
        namespace={namespace || 'default'}
        namespaces={namespaces}
        hint=""
        api={api}
        onClose={onClose}
        onSaved={(s) => onCreated(s.name, s.namespace)}
      />
    )

  const create = async () => {
    setBusy(true)
    setError('')
    try {
      const o = await createFromYaml(clusterId, schema, namespace || undefined, text)
      onCreated(o.metadata.name, o.metadata.namespace)
    } catch (e) {
      setError(errorMessage(e))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Modal title={`Create a ${schema.kind}`} onClose={onClose} wide>
      <ErrorBanner error={error} />
      <CodeEditor value={text} onChange={setText} minHeight="20rem" maxHeight="60vh" upload />
      <div className="mt-3 flex items-center justify-end gap-2">
        <p className="hint mt-0 mr-auto">Created as you; it fails if one with the name exists.</p>
        <button type="button" className="btn btn-secondary" onClick={onClose}>
          Cancel
        </button>
        <button type="button" className="btn btn-primary" disabled={busy} onClick={create}>
          {busy ? 'Creating…' : 'Create'}
        </button>
      </div>
    </Modal>
  )
}
