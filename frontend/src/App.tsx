import { BrowserRouter, Routes, Route } from 'react-router-dom'
import NavSidebar from './components/shared/NavSidebar'
import BoardPage from './pages/BoardPage'
import DashboardPage from './pages/DashboardPage'
import TaskDetailPage from './pages/TaskDetailPage'
import WorkflowPage from './pages/WorkflowPage'
import AgentConfigPage from './pages/AgentConfigPage'
import ReposPage from './pages/ReposPage'

export default function App() {
  return (
    <BrowserRouter>
      <div className="flex h-screen overflow-hidden">
        <NavSidebar />
        <main className="flex-1 overflow-auto bg-slate-950">
          <Routes>
            <Route path="/"            element={<DashboardPage />} />
            <Route path="/board"       element={<BoardPage />} />
            <Route path="/tasks/:id"   element={<TaskDetailPage />} />
            <Route path="/workflow"    element={<WorkflowPage />} />
            <Route path="/agents"      element={<AgentConfigPage />} />
            <Route path="/repos"       element={<ReposPage />} />
          </Routes>
        </main>
      </div>
    </BrowserRouter>
  )
}
