import LessonsPanel from '../components/lessons/LessonsPanel'

export default function LessonsSettingsPage() {
  return (
    <div className="p-4 md:p-6 max-w-3xl">
      <h2 className="text-lg font-semibold text-slate-100 mb-1">Global lessons</h2>
      <p className="text-sm text-slate-400 mb-4">Apply to every repo. Repo-specific lessons live on the Repos page.</p>
      <LessonsPanel repoId={null} title="Global lessons" />
    </div>
  )
}
