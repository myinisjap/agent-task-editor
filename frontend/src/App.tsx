import { BrowserRouter, Routes, Route } from 'react-router-dom'
import NavSidebar from './components/shared/NavSidebar'
import ApiTokenGate from './components/shared/ApiTokenGate'
import BoardPage from './pages/BoardPage'
import DashboardPage from './pages/DashboardPage'
import UsagePage from './pages/UsagePage'
import AgentPerformancePage from './pages/AgentPerformancePage'
import TaskDetailPage from './pages/TaskDetailPage'
import WorkflowPage from './pages/WorkflowPage'
import AgentConfigPage from './pages/AgentConfigPage'
import ReposPage from './pages/ReposPage'
import HealthPage from './pages/HealthPage'

export default function App() {
  return (
    <BrowserRouter basename={import.meta.env.BASE_URL}>
      <ApiTokenGate>
        <div className="flex h-screen overflow-hidden">
          <NavSidebar />
          <main className="flex-1 overflow-auto bg-slate-950 pt-12 md:pt-0">
            <Routes>
              <Route path="/"                      element={<DashboardPage />} />
              <Route path="/dashboard/usage"       element={<UsagePage />} />
              <Route path="/dashboard/performance" element={<AgentPerformancePage />} />
              <Route path="/board"                 element={<BoardPage />} />
              <Route path="/tasks/:id"             element={<TaskDetailPage />} />
              <Route path="/workflow"              element={<WorkflowPage />} />
              <Route path="/agents"                element={<AgentConfigPage />} />
              <Route path="/repos"                 element={<ReposPage />} />
              <Route path="/health"                element={<HealthPage />} />
            </Routes>
          </main>
        </div>
      </ApiTokenGate>
    </BrowserRouter>
  )
}
