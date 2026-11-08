// Adds a shared Resource to a template by reference, from the Resource's
// page: the template's resources get { name, ref } (and refNamespace when
// the Resource is in another namespace), saved to the template at once.
import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { errorMessage } from '../lib/api'
import { useLoad } from '../lib/hooks'
import { idOf, kube, save, type Obj } from '../lib/kube'
import type { Entry } from '../lib/resources'
import { paths } from '../lib/routes'
import { TYPES } from '../lib/types'
import { SelectField, TextField } from './form'
import { Modal } from './Modal'
import { ErrorBanner, Loading } from './ui'

const refers = (e: Entry, res: Obj, tplNs: string) =>
  e.ref === res.metadata.name && (e.refNamespace || tplNs) === res.metadata.namespace

export function AddToTemplate({ resource, onClose }: { resource: Obj; onClose: () => void }) {
  const navigate = useNavigate()
  const { data, error, loading } = useLoad((r) => r.findAll(TYPES.TEMPLATE), [])
  const [picked, setPicked] = useState('')
  const [name, setName] = useState('')
  const [busy, setBusy] = useState(false)
  const [saveError, setSaveError] = useState('')
  const templates = data || []

  const add = async () => {
    setBusy(true)
    setSaveError('')
    try {
      const [tns, tname] = picked.split('/')
      // Read it fresh, so the save doesn't drop someone else's change.
      const tpl: Obj = JSON.parse(JSON.stringify(await kube.find(TYPES.TEMPLATE, tns, tname)))
      const list: Entry[] = tpl.spec?.resources || []
      const entryName = name.trim() || resource.metadata.name
      if (list.some((e) => e.name === entryName)) throw new Error(`${tname} already has a resource named ${entryName}; give this one another name.`)
      const e: Entry = { name: entryName, ref: resource.metadata.name }
      if (resource.metadata.namespace !== tns) e.refNamespace = resource.metadata.namespace
      tpl.spec = { ...(tpl.spec || {}), resources: [...list, e] }
      await save(TYPES.TEMPLATE, tpl)
      navigate(paths.template(tns, tname))
    } catch (err) {
      setSaveError(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Modal title={`Add ${resource.metadata.name} to a template`} onClose={onClose}>
      {loading ? (
        <Loading />
      ) : (
        <div className="space-y-3">
          <ErrorBanner error={error} />
          <ErrorBanner error={saveError} />
          <SelectField
            label="Template"
            value={picked}
            onChange={setPicked}
            placeholder="Choose a template"
            options={templates.map((t) => {
              const has = (t.spec?.resources || []).some((e: Entry) => refers(e, resource, t.metadata.namespace!))
              return { value: idOf(t), label: `${idOf(t)}${has ? ' (has it)' : ''}`, disabled: has }
            })}
          />
          <TextField label="Name in the template" value={name} onChange={setName} placeholder={resource.metadata.name} />
          <p className="hint mt-0">Every cluster made from the template gets it, now and later. Value overrides can be set on the template's page.</p>
          <div className="flex justify-end gap-2">
            <button type="button" className="btn btn-secondary" onClick={onClose}>
              Cancel
            </button>
            <button type="button" className="btn btn-primary" disabled={busy || !picked} onClick={add}>
              {busy ? 'Adding…' : 'Add'}
            </button>
          </div>
        </div>
      )}
    </Modal>
  )
}
