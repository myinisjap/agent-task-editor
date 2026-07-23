import { BrowserRouter, Routes, Route } from 'react-router-dom'
import NavSidebar from './components/shared/NavSidebar'
import ApiTokenGate from './components/shared/ApiTokenGate'
import ErrorBoundary from './components/shared/ErrorBoundary'
import { useHumanNeededNotifications } from './lib/useHumanNeededNotifications'
import BoardPage from './pages/BoardPage'
import ChatPage from './pages/ChatPage'
import DashboardPage from './pages/DashboardPage'
import UsagePage from './pages/UsagePage'
import AgentPerformancePage from './pages/AgentPerformancePage'
import TaskDetailPage from './pages/TaskDetailPage'
import WorkflowPage from './pages/WorkflowPage'
import AgentConfigPage from './pages/AgentConfigPage'
import ProviderConfigPage from './pages/ProviderConfigPage'
import PricingSettingsPage from './pages/PricingSettingsPage'
import ReposPage from './pages/ReposPage'
import TemplatesPage from './pages/TemplatesPage'
import HealthPage from './pages/HealthPage'

export default function App() {
  // Registered once at the app root (not per-page) so "human needed"
  // notifications fire for the whole session regardless of route.
  useHumanNeededNotifications()

  return (
    <BrowserRouter basename={import.meta.env.BASE_URL}>
      <ApiTokenGate>
        {/* h-dvh (dynamic viewport height) not h-screen/100vh: on mobile 100vh
            includes the area behind the address bar and keyboard, which pushes
            fixed-bottom UI (e.g. the chat composer) below the fold. dvh tracks
            the actually-visible height. */}
        <div className="flex h-dvh overflow-hidden">
          <NavSidebar />
          <main className="flex-1 overflow-auto bg-slate-950 pt-12 md:pt-0">
            <ErrorBoundary>
            <Routes>
              <Route path="/"                      element={<DashboardPage />} />
              <Route path="/dashboard/usage"       element={<UsagePage />} />
              <Route path="/dashboard/performance" element={<AgentPerformancePage />} />
              <Route path="/board"                 element={<BoardPage />} />
              <Route path="/chat"                  element={<ChatPage />} />
              <Route path="/tasks/:id"             element={<TaskDetailPage />} />
              <Route path="/workflow"              element={<WorkflowPage />} />
              <Route path="/agents"                element={<AgentConfigPage />} />
              <Route path="/providers"             element={<ProviderConfigPage />} />
              <Route path="/settings/pricing"      element={<PricingSettingsPage />} />
              <Route path="/repos"                 element={<ReposPage />} />
              <Route path="/templates"             element={<TemplatesPage />} />
              <Route path="/health"                element={<HealthPage />} />
            </Routes>
            </ErrorBoundary>
          </main>
        </div>
      </ApiTokenGate>
    </BrowserRouter>
  )
}
