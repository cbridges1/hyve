import { Navigate, Route, HashRouter, Routes } from 'react-router-dom'
import { AppShell } from './components/AppShell'
import { LoginForm } from './components/LoginForm'
import { AdminOnly, SuperadminOnly } from './components/RoleGate'
import { ConfirmProvider } from './lib/confirm'
import { useSession } from './lib/useAuth'
import { ClusterDetailPage } from './routes/ClusterDetailPage'
import { ClustersListPage } from './routes/ClustersListPage'
import { EnvironmentsPage } from './routes/EnvironmentsPage'
import { ForgotPasswordPage } from './routes/ForgotPasswordPage'
import { ModuleDetailPage } from './routes/ModuleDetailPage'
import { ModulesPage } from './routes/ModulesPage'
import { OrganizationsPage } from './routes/OrganizationsPage'
import { OrgReconcilingClusterPage } from './routes/OrgReconcilingClusterPage'
import { ResetPasswordPage } from './routes/ResetPasswordPage'
import { ResourceDetailPage } from './routes/ResourceDetailPage'
import { ResourcesPage } from './routes/ResourcesPage'
import { SecretsPage } from './routes/SecretsPage'
import { SettingsRoute } from './routes/SettingsRoute'
import { TemplateDetailPage } from './routes/TemplateDetailPage'
import { TemplatesPage } from './routes/TemplatesPage'
import { UserDetailPage } from './routes/UserDetailPage'
import { UsersPage } from './routes/UsersPage'
import { WorkflowDetailPage } from './routes/WorkflowDetailPage'
import { WorkflowsPage } from './routes/WorkflowsPage'

function App() {
  const session = useSession()

  // HashRouter, not BrowserRouter: this is meant to be deployable as a
  // plain static site (GitHub Pages-style, no server-side rewrite rules
  // available) — same deployment model hyve-studio's own vite.config.ts
  // assumed, see Phase 11's own notes.
  //
  // /forgot-password and /reset-password are mounted unconditionally,
  // outside the session gate below — a password-reset email's link has
  // to land somewhere reachable whether or not the browser opening it
  // happens to have an existing session (see
  // HYVE-EMAIL-IMPLEMENTATION-PLAN.md's Milestone 4: this is the one
  // place that plan touches something load-bearing rather than purely
  // additive — the session check used to gate the router itself, with no
  // URL space at all for an unauthenticated route).
  return (
    <ConfirmProvider>
      <HashRouter>
        <Routes>
          <Route path="/forgot-password" element={<ForgotPasswordPage />} />
          <Route path="/reset-password" element={<ResetPasswordPage />} />
          {!session ? (
            <Route path="*" element={<LoginForm />} />
          ) : (
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
                  <AdminOnly>
                    <OrgReconcilingClusterPage />
                  </AdminOnly>
                }
              />
              <Route path="/environments" element={<EnvironmentsPage />} />
              <Route path="/resources" element={<ResourcesPage />} />
              <Route path="/resources/:name" element={<ResourceDetailPage />} />
              <Route path="/modules" element={<ModulesPage />} />
              <Route path="/modules/:name" element={<ModuleDetailPage />} />
              <Route path="/secrets" element={<SecretsPage />} />
              <Route
                path="/users"
                element={
                  <AdminOnly>
                    <UsersPage />
                  </AdminOnly>
                }
              />
              <Route
                path="/users/:username"
                element={
                  <AdminOnly>
                    <UserDetailPage />
                  </AdminOnly>
                }
              />
              <Route
                path="/settings"
                element={
                  <AdminOnly>
                    <SettingsRoute />
                  </AdminOnly>
                }
              />
              <Route path="*" element={<Navigate to="/clusters" replace />} />
            </Route>
          )}
        </Routes>
      </HashRouter>
    </ConfirmProvider>
  )
}

export default App
