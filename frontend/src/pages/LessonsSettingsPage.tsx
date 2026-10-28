import LessonsPanel from '../components/lessons/LessonsPanel'
import { PageFrame } from '../components/ui'

export default function LessonsSettingsPage() {
  return (
    <PageFrame title="Global lessons" description="Apply to every repo. Repo-specific lessons live on the Repos page.">
      <LessonsPanel repoId={null} title="Lessons" />
    </PageFrame>
  )
}
