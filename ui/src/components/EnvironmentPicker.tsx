import { useEffect } from 'react'
import { useEnvironment, useEnvironments } from '../lib/environment'

// Picks the environment hyve's pages show, or all of them.
export function EnvironmentPicker() {
  const [env, setEnv] = useEnvironment()
  const { data: envs } = useEnvironments()
  // A remembered environment that's gone (or not visible) means all.
  useEffect(() => {
    if (envs && env && !envs.includes(env)) setEnv('')
  }, [envs, env, setEnv])
  return (
    <label className="flex items-center gap-2 text-sm text-neutral-600 dark:text-neutral-400">
      <span className="whitespace-nowrap">Environment</span>
      <select className="min-w-[10rem]" value={env} onChange={(e) => setEnv(e.target.value)}>
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
