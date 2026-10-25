// hyve's UI, served by the host (HYVE-PROPOSAL.md, "UI: on or off at
// install").
import { useEffect, useState, type ReactNode } from 'react'
import { BrowserRouter, Navigate, Route, Routes, useLocation } from 'react-router-dom'
import { AppShell } from './components/AppShell'
import { Loading } from './components/ui'
import { auth, setUnauthorizedHandler } from './lib/api'
import { stopLive } from './lib/live'
import { paths } from './lib/routes'
import { getSession, rememberCLIServer, setSession, useSession } from './lib/session'
import { startSession } from './lib/start'
import Access from './pages/Access'
import CliLogin from './pages/CliLogin'
import ClusterDetail from './pages/ClusterDetail'
import ClusterList from './pages/ClusterList'
import CreateCluster from './pages/CreateCluster'
import Environments from './pages/Environments'
import GenericList from './pages/GenericList'
import ImportPage from './pages/ImportPage'
import ManagementExplorer from './pages/ManagementExplorer'
import Login from './pages/Login'
import Overview from './pages/Overview'
import Providers from './pages/Providers'
import Repos from './pages/Repos'
import ResourceMatrix from './pages/ResourceMatrix'
import Secrets from './pages/Secrets'
import SharedResourceList from './pages/SharedResourceList'
import SharedResourcePage from './pages/SharedResourcePage'
import TemplateList from './pages/TemplateList'
import TemplatePage from './pages/TemplatePage'
import Users from './pages/Users'
import YamlPage from './pages/YamlPage'

// `hyve ui` passes a session in the fragment, which never reaches a
// server; trade it for the host's cookie and drop it from the URL.
async function signInFromFragment() {
  const hash = window.location.hash
  const m = /(?:^#|&)session=([^&]+)/.exec(hash)
  if (!m) return
  const server = /(?:^#|&)server=([^&]+)/.exec(hash)
  const url = /(?:^#|&)url=([^&]+)/.exec(hash)
  if (server && url) rememberCLIServer(decodeURIComponent(server[1]), decodeURIComponent(url[1]))
  window.history.replaceState(null, '', window.location.pathname + window.location.search)
  await auth.redeem(decodeURIComponent(m[1])).catch(() => {})
}

async function boot() {
  await signInFromFragment()
  try {
    // Local mode signs in without a password; elsewhere this 404s and the
    // sign-in page shows.
    await auth.me().catch((e) => auth.local().catch(() => Promise.reject(e)))
    await startSession()
  } catch {
    setSession({ user: null })
  }
}

function RequireAuth({ children }: { children: ReactNode }) {
  const { user } = useSession()
  const location = useLocation()
  if (!user) {
    const redirect = location.pathname + location.search
    return <Navigate to={redirect === '/' ? '/login' : `/login?redirect=${encodeURIComponent(redirect)}`} replace />
  }
  return <>{children}</>
}

export default function App() {
  const [ready, setReady] = useState(false)

  useEffect(() => {
    setUnauthorizedHandler(async () => {
      const { user } = getSession()
      if (!user) return
      // Local mode: the session ran out; start another and reload.
      if (user.autoSignIn && (await auth.local().then(() => true, () => false))) {
        window.location.reload()
        return
      }
      stopLive()
      setSession({ user: null })
    })
    boot().finally(() => setReady(true))
  }, [])

  if (!ready) return <Loading />

  return (
    <BrowserRouter>
      <Routes>
        <Route path="/login" element={<Login />} />
        <Route path="/cli-login" element={<CliLogin />} />
        <Route
          element={
            <RequireAuth>
              <AppShell />
            </RequireAuth>
          }
        >
          <Route index element={<Navigate to={paths.overview} replace />} />
          <Route path="overview" element={<Overview />} />
          <Route path="clusters" element={<ClusterList />} />
          <Route path="clusters/new" element={<CreateCluster />} />
          <Route path="clusters/:namespace/:name/:tab?/*" element={<ClusterDetail />} />
          <Route path="templates" element={<TemplateList />} />
          <Route path="templates/new" element={<TemplatePage />} />
          <Route path="templates/:namespace/:name" element={<TemplatePage />} />
          <Route path="resources" element={<SharedResourceList />} />
          <Route path="resources/new" element={<SharedResourcePage />} />
          <Route path="resources/:namespace/:name" element={<SharedResourcePage />} />
          <Route path="matrix" element={<ResourceMatrix />} />
          <Route path="secrets" element={<Secrets />} />
          <Route path="repos" element={<Repos />} />
          <Route path="providers" element={<Providers />} />
          <Route path="users" element={<Users />} />
          <Route path="environments" element={<Environments />} />
          <Route path="access" element={<Access />} />
          <Route path="import" element={<ImportPage />} />
          <Route path="explorer" element={<ManagementExplorer />} />
          <Route path="list/:type" element={<GenericList />} />
          <Route path="yaml/:type/new" element={<YamlPage />} />
          <Route path="yaml/:type/:namespace/:name" element={<YamlPage />} />
          <Route path="*" element={<Navigate to={paths.overview} replace />} />
        </Route>
      </Routes>
    </BrowserRouter>
  )
}
