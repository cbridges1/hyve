import { Navigate, Route, HashRouter, Routes } from 'react-router-dom'
import { AppShell } from './components/AppShell'
import { LoginForm } from './components/LoginForm'
import { useSession } from './lib/useAuth'
import { ClusterDetailPage } from './routes/ClusterDetailPage'
import { ClustersListPage } from './routes/ClustersListPage'
import { ModulesPage } from './routes/ModulesPage'
import { ResourcesPage } from './routes/ResourcesPage'
import { SecretsPage } from './routes/SecretsPage'
import { TemplatesPage } from './routes/TemplatesPage'
import { WorkflowsPage } from './routes/WorkflowsPage'

function App() {
  const session = useSession()

  if (!session) return <LoginForm />

  // HashRouter, not BrowserRouter: this is meant to be deployable as a
  // plain static site (GitHub Pages-style, no server-side rewrite rules
  // available) — same deployment model hyve-studio's own vite.config.ts
  // assumed, see Phase 11's own notes.
  return (
    <HashRouter>
      <Routes>
        <Route element={<AppShell />}>
          <Route path="/" element={<Navigate to="/clusters" replace />} />
          <Route path="/clusters" element={<ClustersListPage />} />
          <Route path="/clusters/:name" element={<ClusterDetailPage />} />
          <Route path="/templates" element={<TemplatesPage />} />
          <Route path="/workflows" element={<WorkflowsPage />} />
          <Route path="/resources" element={<ResourcesPage />} />
          <Route path="/modules" element={<ModulesPage />} />
          <Route path="/secrets" element={<SecretsPage />} />
          <Route path="*" element={<Navigate to="/clusters" replace />} />
        </Route>
      </Routes>
    </HashRouter>
  )
}

export default App
