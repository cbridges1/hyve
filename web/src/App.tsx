import { Navigate, Route, HashRouter, Routes } from 'react-router-dom'
import { AppShell } from './components/AppShell'
import { LoginForm } from './components/LoginForm'
import { SuperadminOnly } from './components/RoleGate'
import { ConfirmProvider } from './lib/confirm'
import { useSession } from './lib/useAuth'
import { AccountsPage } from './routes/AccountsPage'
import { ClusterDetailPage } from './routes/ClusterDetailPage'
import { ClustersListPage } from './routes/ClustersListPage'
import { ModuleDetailPage } from './routes/ModuleDetailPage'
import { ModulesPage } from './routes/ModulesPage'
import { OrganizationsPage } from './routes/OrganizationsPage'
import { ReconcilingClustersPage } from './routes/ReconcilingClustersPage'
import { ResourceDetailPage } from './routes/ResourceDetailPage'
import { ResourcesPage } from './routes/ResourcesPage'
import { SecretsPage } from './routes/SecretsPage'
import { SettingsPage } from './routes/SettingsPage'
import { TemplateDetailPage } from './routes/TemplateDetailPage'
import { TemplatesPage } from './routes/TemplatesPage'
import { WorkflowDetailPage } from './routes/WorkflowDetailPage'
import { WorkflowsPage } from './routes/WorkflowsPage'

function App() {
  const session = useSession()

  if (!session) return <LoginForm />

  // HashRouter, not BrowserRouter: this is meant to be deployable as a
  // plain static site (GitHub Pages-style, no server-side rewrite rules
  // available) — same deployment model hyve-studio's own vite.config.ts
  // assumed, see Phase 11's own notes.
  return (
    <ConfirmProvider>
      <HashRouter>
        <Routes>
          <Route element={<AppShell />}>
            <Route path="/" element={<Navigate to="/clusters" replace />} />
            <Route path="/clusters" element={<ClustersListPage />} />
            <Route path="/clusters/:name" element={<ClusterDetailPage />} />
            <Route path="/templates" element={<TemplatesPage />} />
            <Route path="/templates/:name" element={<TemplateDetailPage />} />
            <Route path="/workflows" element={<WorkflowsPage />} />
            <Route path="/workflows/:name" element={<WorkflowDetailPage />} />
            <Route
              path="/organizations"
              element={
                <SuperadminOnly>
                  <OrganizationsPage />
                </SuperadminOnly>
              }
            />
            <Route
              path="/reconciling-clusters"
              element={
                <SuperadminOnly>
                  <ReconcilingClustersPage />
                </SuperadminOnly>
              }
            />
            <Route path="/resources" element={<ResourcesPage />} />
            <Route path="/resources/:name" element={<ResourceDetailPage />} />
            <Route path="/modules" element={<ModulesPage />} />
            <Route path="/modules/:name" element={<ModuleDetailPage />} />
            <Route path="/secrets" element={<SecretsPage />} />
            <Route path="/accounts" element={<AccountsPage />} />
            <Route
              path="/settings"
              element={
                <SuperadminOnly>
                  <SettingsPage />
                </SuperadminOnly>
              }
            />
            <Route path="*" element={<Navigate to="/clusters" replace />} />
          </Route>
        </Routes>
      </HashRouter>
    </ConfirmProvider>
  )
}

export default App
