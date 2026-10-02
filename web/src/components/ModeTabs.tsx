/** Two-way tab switch used by "New X" modals to toggle between a structured form and raw-YAML advanced mode. */
export function ModeTabs<T extends string>({
  value,
  onChange,
  options,
}: {
  value: T
  onChange: (v: T) => void
  options: { value: T; label: string }[]
}) {
  return (
    <div className="mb-4 flex gap-1 rounded-lg bg-neutral-100 p-1 text-sm dark:bg-neutral-900">
      {options.map((opt) => (
        <button
          key={opt.value}
          type="button"
          onClick={() => onChange(opt.value)}
          className={`flex-1 rounded-md px-3 py-1.5 font-medium transition-colors ${
            value === opt.value
              ? 'bg-white text-neutral-900 shadow-sm dark:bg-neutral-700 dark:text-neutral-100'
              : 'text-neutral-500 hover:text-neutral-700 dark:hover:text-neutral-300'
          }`}
        >
          {opt.label}
        </button>
      ))}
    </div>
  )
}
