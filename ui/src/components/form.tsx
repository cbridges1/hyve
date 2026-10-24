// Labelled form controls, in the console's style.
import { useId, useState, type ReactNode } from 'react'

function Label({ id, label, required, hint }: { id: string; label?: ReactNode; required?: boolean; hint?: string }) {
  if (!label) return null
  return (
    <label htmlFor={id} className="label">
      {label}
      {required && <span className="ml-0.5 text-red-500">*</span>}
      {hint && (
        <span
          title={hint}
          className="ml-1.5 inline-flex h-4 w-4 cursor-help items-center justify-center rounded-full border border-neutral-300 text-[10px] text-neutral-500 dark:border-neutral-700"
        >
          ?
        </span>
      )}
    </label>
  )
}

type Common = { label?: ReactNode; required?: boolean; hint?: string; help?: ReactNode; className?: string; disabled?: boolean }

export function TextField({
  value,
  onChange,
  placeholder,
  type = 'text',
  autoFocus,
  autoComplete,
  list,
  ...c
}: Common & {
  value: string
  onChange: (v: string) => void
  placeholder?: string
  type?: string
  autoFocus?: boolean
  autoComplete?: string
  list?: string[]
}) {
  const id = useId()
  return (
    <div className={c.className}>
      <Label id={id} label={c.label} required={c.required} hint={c.hint} />
      <input
        id={id}
        type={type}
        value={value}
        placeholder={placeholder}
        disabled={c.disabled}
        autoFocus={autoFocus}
        autoComplete={autoComplete}
        list={list ? `${id}-list` : undefined}
        spellCheck={false}
        onChange={(e) => onChange(e.target.value)}
      />
      {list && (
        <datalist id={`${id}-list`}>
          {list.map((o) => (
            <option key={o} value={o} />
          ))}
        </datalist>
      )}
      {c.help && <span className="hint">{c.help}</span>}
    </div>
  )
}

export function TextArea({
  value,
  onChange,
  rows = 4,
  placeholder,
  mono,
  ...c
}: Common & { value: string; onChange: (v: string) => void; rows?: number; placeholder?: string; mono?: boolean }) {
  const id = useId()
  return (
    <div className={c.className}>
      <Label id={id} label={c.label} required={c.required} hint={c.hint} />
      <textarea
        id={id}
        rows={rows}
        value={value}
        placeholder={placeholder}
        disabled={c.disabled}
        spellCheck={false}
        className={mono ? 'font-mono text-xs' : undefined}
        onChange={(e) => onChange(e.target.value)}
      />
      {c.help && <span className="hint">{c.help}</span>}
    </div>
  )
}

export type Option = string | { label: string; value: string; disabled?: boolean }

const opt = (o: Option) => (typeof o === 'string' ? { label: o, value: o } : o)

export function SelectField({
  value,
  onChange,
  options,
  placeholder,
  ...c
}: Common & { value: string; onChange: (v: string) => void; options: Option[]; placeholder?: string }) {
  const id = useId()
  const opts = options.map(opt)
  return (
    <div className={c.className}>
      <Label id={id} label={c.label} required={c.required} hint={c.hint} />
      <select id={id} value={value} disabled={c.disabled} onChange={(e) => onChange(e.target.value)}>
        {placeholder !== undefined && <option value="">{placeholder}</option>}
        {value && !opts.some((o) => o.value === value) && <option value={value}>{value}</option>}
        {opts.map((o) => (
          <option key={o.value} value={o.value} disabled={o.disabled}>
            {o.label}
          </option>
        ))}
      </select>
      {c.help && <span className="hint">{c.help}</span>}
    </div>
  )
}

export function Checkbox({ checked, onChange, label, disabled }: { checked: boolean; onChange: (v: boolean) => void; label: ReactNode; disabled?: boolean }) {
  return (
    <label className="flex cursor-pointer items-center gap-2 text-sm">
      <input type="checkbox" checked={checked} disabled={disabled} onChange={(e) => onChange(e.target.checked)} />
      {label}
    </label>
  )
}

export function RadioGroup({ value, onChange, options, label }: { value: string; onChange: (v: string) => void; options: Option[]; label?: ReactNode }) {
  const name = useId()
  return (
    <div>
      {label && <span className="label">{label}</span>}
      <div className="flex flex-col gap-1.5 text-sm">
        {options.map(opt).map((o) => (
          <label key={o.value} className="flex cursor-pointer items-center gap-2">
            <input type="radio" name={name} checked={value === o.value} disabled={o.disabled} onChange={() => onChange(o.value)} />
            {o.label}
          </label>
        ))}
      </div>
    </div>
  )
}

// A map of strings as rows of key and value.
export function KeyValueEditor({
  value,
  onChange,
  addLabel = 'Add',
  keyPlaceholder = 'key',
  valuePlaceholder = 'value',
  readOnly,
}: {
  value: Record<string, string>
  onChange: (v: Record<string, string>) => void
  addLabel?: string
  keyPlaceholder?: string
  valuePlaceholder?: string
  readOnly?: boolean
}) {
  const [rows, setRows] = useState(() => Object.entries(value || {}).map(([k, v]) => ({ k, v })))
  const emit = (next: { k: string; v: string }[]) => {
    setRows(next)
    const out: Record<string, string> = {}
    next.filter((r) => r.k).forEach((r) => (out[r.k] = r.v))
    onChange(out)
  }
  if (readOnly && !rows.length) return <p className="text-muted text-sm">—</p>
  return (
    <div className="space-y-2">
      {rows.map((r, i) => (
        <div key={i} className="flex gap-2">
          <input
            className="flex-[5]"
            value={r.k}
            placeholder={keyPlaceholder}
            disabled={readOnly}
            onChange={(e) => emit(rows.map((x, j) => (j === i ? { ...x, k: e.target.value } : x)))}
          />
          <input
            className="flex-[6]"
            value={r.v}
            placeholder={valuePlaceholder}
            disabled={readOnly}
            onChange={(e) => emit(rows.map((x, j) => (j === i ? { ...x, v: e.target.value } : x)))}
          />
          {!readOnly && (
            <button type="button" className="btn btn-tertiary" onClick={() => emit(rows.filter((_, j) => j !== i))}>
              Remove
            </button>
          )}
        </div>
      ))}
      {!readOnly && (
        <button type="button" className="btn btn-tertiary" onClick={() => setRows([...rows, { k: '', v: '' }])}>
          {addLabel}
        </button>
      )}
    </div>
  )
}
