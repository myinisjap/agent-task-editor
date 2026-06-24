import { useParams } from 'react-router-dom'

export default function TaskDetailPage() {
  const { id } = useParams<{ id: string }>()
  return (
    <div className="p-6">
      <h1 className="text-2xl font-semibold text-slate-100 mb-4">Task {id}</h1>
      <p className="text-slate-400">Task detail — Phase 7</p>
    </div>
  )
}
