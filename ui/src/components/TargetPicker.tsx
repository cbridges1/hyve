// Where a Deployment goes, in plain terms: templates, cluster classes,
// environments, single clusters, groups, all clusters or the host. Each
// becomes a Fleet target (lib/delivery.ts); nobody writes a selector.
// Targets hyve doesn't model (written by hand) show as "Custom target" and
// are kept.
import { useState } from 'react'
import { idOf, type Obj } from '../lib/kube'
import { groupKind, TARGET_KINDS, targetKey, targetLabel, type Target, type TargetKind } from '../lib/delivery'
import { SelectField } from './form'

export type TargetOptions = {
  templates: Obj[]
  classes: string[]
  environments: string[]
  clusters: Obj[]
  groups: Obj[]
}

export function TargetPicker({
  value,
  onChange,
  options,
  disabled,
  only,
}: {
  value: Target[]
  onChange: (ts: Target[]) => void
  options: TargetOptions
  disabled?: boolean
  // The kinds offered (an environment's Deployment: its clusters only).
  only?: TargetKind[]
}) {
  const kinds = TARGET_KINDS.filter((k) => !only || only.includes(k.kind))
  const [kind, setKind] = useState<TargetKind>(kinds[0]?.kind || 'template')
  const [name, setName] = useState('')

  const names: { label: string; value: string }[] = (() => {
    switch (kind) {
      case 'template':
        return options.templates.map((t) => ({ label: t.metadata.name, value: t.metadata.name }))
      case 'class':
        return options.classes.map((c) => ({ label: c, value: c }))
      case 'env':
        return options.environments.map((e) => ({ label: e, value: e }))
      case 'cluster':
        return options.clusters.map((c) => ({ label: idOf(c), value: idOf(c) }))
      case 'group':
        return options.groups.filter((g) => groupKind(g) === 'custom').map((g) => ({ label: g.metadata.name, value: g.metadata.name }))
    }
    return []
  })()
  const needsName = kind !== 'all' && kind !== 'host'

  const add = () => {
    let t: Target
    if (!needsName) t = { kind }
    else if (kind === 'cluster') {
      const [env, n] = name.split('/')
      t = { kind, env, name: n }
    } else t = { kind, name }
    if (needsName && !name) return
    if (value.some((v) => targetKey(v) === targetKey(t))) return
    onChange([...value, t])
    setName('')
  }

  return (
    <div>
      <div className="mb-2 flex flex-wrap gap-2">
        {!value.length && <span className="text-muted text-sm">No targets yet.</span>}
        {value.map((t) => (
          <span key={targetKey(t)} className="inline-flex items-center gap-1 rounded-full border border-neutral-300 px-2.5 py-0.5 text-sm dark:border-neutral-700" title={t.kind === 'custom' ? JSON.stringify(t.raw) : undefined}>
            {targetLabel(t)}
            {!disabled && (
              <button type="button" aria-label={`Remove ${targetLabel(t)}`} className="text-muted hover:text-current" onClick={() => onChange(value.filter((v) => targetKey(v) !== targetKey(t)))}>
                ×
              </button>
            )}
          </span>
        ))}
      </div>
      {!disabled && (
        <div className="flex flex-wrap items-end gap-2">
          <SelectField
            label="Deliver to"
            value={kind}
            onChange={(v) => {
              setKind(v as TargetKind)
              setName('')
            }}
            options={kinds.map((k) => ({ label: k.label, value: k.kind }))}
          />
          {needsName && (
            <SelectField
              label={kinds.find((k) => k.kind === kind)?.label || 'Name'}
              value={name}
              onChange={setName}
              options={[{ label: names.length ? 'Pick one' : 'None yet', value: '' }, ...names]}
            />
          )}
          <button type="button" className="btn btn-secondary mb-0.5" disabled={needsName && !name} onClick={add}>
            Add
          </button>
        </div>
      )}
    </div>
  )
}
