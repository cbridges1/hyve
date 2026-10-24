import { useTheme, type Theme } from '../lib/theme'
import { Icon } from './Icon'

const options: { value: Theme; label: string; icon: string }[] = [
  { value: 'light', label: 'Light theme', icon: 'sun' },
  { value: 'dark', label: 'Dark theme', icon: 'moon' },
  { value: 'system', label: 'Match system theme', icon: 'system' },
]

// Three-way light/dark/system segmented control.
export function ThemeToggle() {
  const [theme, setTheme] = useTheme()
  return (
    <div className="flex gap-1 rounded-full bg-neutral-100 p-1 dark:bg-neutral-900" role="radiogroup" aria-label="Theme">
      {options.map(({ value, label, icon }) => (
        <button
          key={value}
          type="button"
          role="radio"
          aria-checked={theme === value}
          title={label}
          onClick={() => setTheme(value)}
          className={`flex items-center justify-center rounded-full p-2 transition-colors ${
            theme === value
              ? 'bg-white text-neutral-900 shadow-sm dark:bg-neutral-700 dark:text-neutral-100'
              : 'text-neutral-500 hover:text-neutral-700 dark:hover:text-neutral-300'
          }`}
        >
          <Icon name={icon} size={16} />
        </button>
      ))}
    </div>
  )
}
