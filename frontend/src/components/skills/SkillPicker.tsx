import { Link, useInRouterContext } from 'react-router-dom'
import type { Skill } from '../../api/client'
import ChipPicker from '../shared/ChipPicker'

// SkillPicker is a toggle-chip multi-select over the skills library. Disabled
// skills stay selectable (so attachments survive toggling) but are labelled,
// since the run-start copy skips them.
export default function SkillPicker({ skills, selected, onChange }: {
  skills: Skill[]
  selected: string[]
  onChange: (ids: string[]) => void
}) {
  const inRouter = useInRouterContext()
  const known = new Set(skills.map((s) => s.id))
  const available = skills.map((s) => ({ value: s.id, label: s.enabled === false ? `${s.name} (disabled)` : s.name }))
  const hasDisabled = skills.some((s) => s.enabled === false)
  return (
    <div>
      <ChipPicker
        selected={selected.filter((id) => known.has(id))}
        available={available}
        onChange={onChange}
        emptyMessage="No skills yet — add them under Settings → Skills."
      />
      {hasDisabled && <p className="mt-1 text-xs text-slate-500">Disabled skills are not copied into the worktree.</p>}
      <p className="mt-1 text-xs">
        {inRouter ? (
          <Link to="/settings/skills" className="text-indigo-400 hover:underline">Manage skills</Link>
        ) : (
          <a href="/settings/skills" className="text-indigo-400 hover:underline">Manage skills</a>
        )}
      </p>
    </div>
  )
}
