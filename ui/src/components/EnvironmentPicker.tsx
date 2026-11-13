import { useEffect } from 'react'
import { useEnvironment, useEnvironments } from '../lib/environment'

// Picks the environment hyve's pages show, or all of them. In the header,
// on the pages that use it (useEnvironmentScope); its label folds away on
// phones.
export function EnvironmentPicker() {
  const [env, setEnv] = useEnvironment()
  const { data: envs } = useEnvironments()
  // A remembered environment that's gone (or not visible) means all.
  useEffect(() => {
    if (envs && env && !envs.includes(env)) setEnv('')
  }, [envs, env, setEnv])
  return (
    <label className="flex min-w-0 items-center gap-2 text-sm text-neutral-600 dark:text-neutral-400">
      <span className="sr-only whitespace-nowrap sm:not-sr-only">Environment</span>
      <select className="w-32! min-w-0 sm:w-40!" value={env} onChange={(e) => setEnv(e.target.value)}>
        <option value="">All environments</option>
        {(envs || []).map((e) => (
          <option key={e} value={e}>
            {e}
          </option>
        ))}
      </select>
    </label>
  )
}
