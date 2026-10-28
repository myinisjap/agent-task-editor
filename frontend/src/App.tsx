import { lazy, Suspense, useState } from 'react'
import { BrowserRouter, Routes, Route, Navigate, useLocation } from 'react-router-dom'
import NavSidebar from './components/shared/NavSidebar'
import BottomTabBar from './components/shared/BottomTabBar'
import ApiTokenGate from './components/shared/ApiTokenGate'
import ErrorBoundary from './components/shared/ErrorBoundary'
import HomeRoute from './components/shared/HomeRoute'
import CommandHost from './components/shared/CommandHost'
import ToastHost from './components/shared/ToastHost'
import { useHealthPolling } from './stores/health'
import ProviderPausedBanner from './components/shared/ProviderPausedBanner'
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
const InboxPage = lazy(() => import('./pages/InboxPage'))
const HealthPage = lazy(() => import('./pages/HealthPage'))
const PipelinePage = lazy(() => import('./pages/PipelinePage'))
const PipelineOverviewPage = lazy(() => import('./pages/PipelineOverviewPage'))
const LessonsSettingsPage = lazy(() => import('./pages/LessonsSettingsPage'))
const SkillsSettingsPage = lazy(() => import('./pages/SkillsSettingsPage'))
const SettingsPage = lazy(() => import('./pages/SettingsPage'))

/** Redirect a retired path to its new home, keeping query string and hash. */
function Redirect({ to }: { to: string }) {
  const { search, hash } = useLocation()
  return <Navigate to={`${to}${search}${hash}`} replace />
}

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
          <Route path="/"                      element={<HomeRoute><DashboardPage /></HomeRoute>} />
          <Route path="/dashboard"             element={<DashboardPage />} />
          <Route path="/dashboard/usage"       element={<UsagePage />} />
          <Route path="/dashboard/performance" element={<AgentPerformancePage />} />
          <Route path="/inbox"                 element={<InboxPage />} />
          <Route path="/board"                 element={<BoardPage />} />
          <Route path="/chat"                  element={<ChatPage />} />
          <Route path="/tasks/:id"             element={<TaskDetailPage />} />
          <Route path="/pipeline" element={<PipelinePage />}>
            <Route index element={<Redirect to="/pipeline/overview" />} />
            <Route path="overview" element={<PipelineOverviewPage />} />
            <Route path="workflow" element={<WorkflowPage />} />
            <Route path="agents"   element={<AgentConfigPage />} />
          </Route>
          <Route path="/settings" element={<SettingsPage />}>
            <Route index element={<Redirect to="/settings/providers" />} />
            <Route path="providers"    element={<ProviderConfigPage />} />
            <Route path="pricing"      element={<PricingSettingsPage />} />
            <Route path="intake-rules" element={<IntakeRulesPage />} />
            <Route path="templates"    element={<TemplatesPage />} />
            <Route path="lessons"      element={<LessonsSettingsPage />} />
            <Route path="skills"       element={<SkillsSettingsPage />} />
            <Route path="health"       element={<HealthPage />} />
          </Route>
          <Route path="/repos"                 element={<ReposPage />} />

          {/* Retired top-level routes: keep bookmarks and old links working. */}
          <Route path="/workflow"     element={<Redirect to="/pipeline/workflow" />} />
          <Route path="/agents"       element={<Redirect to="/pipeline/agents" />} />
          <Route path="/providers"    element={<Redirect to="/settings/providers" />} />
          <Route path="/pricing"      element={<Redirect to="/settings/pricing" />} />
          <Route path="/templates"    element={<Redirect to="/settings/templates" />} />
          <Route path="/intake-rules" element={<Redirect to="/settings/intake-rules" />} />
          <Route path="/health"       element={<Redirect to="/settings/health" />} />
        </Routes>
      </Suspense>
    </ErrorBoundary>
  )
}

export default function App() {
  // Registered once at the app root (not per-page) so "human needed"
  // notifications fire for the whole session regardless of route.
  useHumanNeededNotifications()
  useHealthPolling()
  const [menuOpen, setMenuOpen] = useState(false)

  return (
    <BrowserRouter basename={import.meta.env.BASE_URL}>
      <ApiTokenGate>
        {/* h-dvh (dynamic viewport height) not h-screen/100vh: on mobile 100vh
            includes the area behind the address bar and keyboard, which pushes
            fixed-bottom UI (e.g. the chat composer) below the fold. dvh tracks
            the actually-visible height. */}
        <div className="flex h-dvh overflow-hidden">
          <NavSidebar open={menuOpen} onOpenChange={setMenuOpen} />
          <main className="flex-1 min-h-0 min-w-0 flex flex-col overflow-auto bg-slate-950 pb-[calc(3.5rem+env(safe-area-inset-bottom))] md:pb-0">
            <ProviderPausedBanner />
            <AppRoutes />
          </main>
          <BottomTabBar onMore={() => setMenuOpen(true)} />
          <CommandHost />
          <ToastHost />
        </div>
      </ApiTokenGate>
    </BrowserRouter>
  )
}
