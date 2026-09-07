import { useTheme } from '../lib/useTheme'
import { MoonIcon, SunIcon, SystemIcon } from './icons'
import type { Theme } from '../lib/themeStore'

const options: { value: Theme; label: string; Icon: typeof SunIcon }[] = [
  { value: 'light', label: 'Light theme', Icon: SunIcon },
  { value: 'system', label: 'Match system theme', Icon: SystemIcon },
  { value: 'dark', label: 'Dark theme', Icon: MoonIcon },
]

/** Three-way light/system/dark segmented control, icon-only — lives in the header's own right-hand cluster, so it gets real breathing room rather than the cramped sizing a sidebar-footer placement would force. */
export function ThemeToggle() {
  const [theme, setTheme] = useTheme()

  return (
    <div className="flex gap-1 rounded-full bg-neutral-100 p-1 dark:bg-neutral-900" role="radiogroup" aria-label="Theme">
      {options.map(({ value, label, Icon }) => (
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
          <Icon width={16} height={16} />
        </button>
      ))}
    </div>
  )
}
