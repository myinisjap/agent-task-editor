import { lazy, Suspense } from 'react'
import { BrowserRouter, Routes, Route, useLocation } from 'react-router-dom'
import NavSidebar from './components/shared/NavSidebar'
import ApiTokenGate from './components/shared/ApiTokenGate'
import ErrorBoundary from './components/shared/ErrorBoundary'
import { useHumanNeededNotifications } from './lib/useHumanNeededNotifications'

// Lazy-loaded so heavy per-route dependencies (@xterm/xterm for ChatPage,
// @xyflow/react + dagre for WorkflowPage via WorkflowFlowchart) don't ship in
// the initial bundle for users who only ever open e.g. /board.
const BoardPage = lazy(() => import('./pages/BoardPage'))
const ChatPage = lazy(() => import('./pages/ChatPage'))
const DashboardPage = lazy(() => import('./pages/DashboardPage'))
const UsagePage = lazy(() => import('./pages/UsagePage'))
const AgentPerformancePage = lazy(() => import('./pages/AgentPerformancePage'))
const TaskDetailPage = lazy(() => import('./pages/TaskDetailPage'))
const WorkflowPage = lazy(() => import('./pages/WorkflowPage'))
const AgentConfigPage = lazy(() => import('./pages/AgentConfigPage'))
const ProviderConfigPage = lazy(() => import('./pages/ProviderConfigPage'))
const PricingSettingsPage = lazy(() => import('./pages/PricingSettingsPage'))
const ReposPage = lazy(() => import('./pages/ReposPage'))
const TemplatesPage = lazy(() => import('./pages/TemplatesPage'))
const IntakeRulesPage = lazy(() => import('./pages/IntakeRulesPage'))
const HealthPage = lazy(() => import('./pages/HealthPage'))

function AppRoutes() {
  // Remount the boundary (and the lazy Suspense subtree) per route so a
  // render crash on one page doesn't stick across subsequent navigation —
  // without this, ErrorBoundary's fallback stays up until a full reload
  // because Routes lives inside a single, never-reset boundary instance.
  const location = useLocation()
  return (
    <ErrorBoundary key={location.pathname}>
      <Suspense fallback={<div className="p-6 text-slate-400 text-sm">Loading…</div>}>
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
          <Route path="/intake-rules"          element={<IntakeRulesPage />} />
          <Route path="/health"                element={<HealthPage />} />
        </Routes>
      </Suspense>
    </ErrorBoundary>
  )
}

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
            <AppRoutes />
          </main>
        </div>
      </ApiTokenGate>
    </BrowserRouter>
  )
}
