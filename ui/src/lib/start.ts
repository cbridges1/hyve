import { auth } from './api'
import { loadSchemas } from './kube'
import { startLive } from './live'
import { setSession } from './session'

// Signed in: who, and what the cluster serves.
export async function startSession() {
  const me = await auth.me()
  setSession({ user: me, version: me.version })
  await loadSchemas()
  startLive()
}
