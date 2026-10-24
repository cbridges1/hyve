// The name and namespace of a new object (fixed once it exists): what
// Rancher's NameNsDescription was on the template and Resource forms.
import { useLoad } from '../lib/hooks'
import { TYPES } from '../lib/types'
import { TextField } from './form'

export function NameNamespace({
  name,
  namespace,
  onName,
  onNamespace,
  fixed,
}: {
  name: string
  namespace: string
  onName: (v: string) => void
  onNamespace: (v: string) => void
  fixed: boolean
}) {
  const { data: namespaces } = useLoad(
    async (r) => (fixed ? [] : (await r.findAll(TYPES.NAMESPACE).catch(() => [])).map((n) => n.metadata.name).sort()),
    [fixed],
  )
  return (
    <div className="grid gap-3 sm:grid-cols-2">
      <TextField label="Name" required value={name} onChange={onName} disabled={fixed} autoFocus={!fixed} />
      <TextField label="Namespace" required value={namespace} onChange={onNamespace} disabled={fixed} list={namespaces} />
    </div>
  )
}
