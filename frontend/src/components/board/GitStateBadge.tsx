type GitState = 'none' | 'branched' | 'pushed' | 'pr_open' | 'pr_merged' | 'pr_closed'

function deriveGitState(branch: string, gitState: string): GitState {
  if (!branch) return 'none'
  if (!gitState) return 'branched'
  return gitState as GitState
}

const GIT_STATE_CONFIG: Record<GitState, { label: string; icon: string; className: string }> = {
  none:      { label: 'No branch',  icon: '○',  className: 'text-slate-600' },
  branched:  { label: 'Branch',     icon: '⎇',  className: 'text-slate-400' },
  pushed:    { label: 'Pushed',     icon: '↑',  className: 'text-blue-400' },
  pr_open:   { label: 'PR open',    icon: '⬡',  className: 'text-yellow-400' },
  pr_merged: { label: 'PR merged',  icon: '⬡',  className: 'text-purple-400' },
  pr_closed: { label: 'PR closed',  icon: '⊗',  className: 'text-red-400' },
}

interface GitStateBadgeProps {
  branch?: string
  gitState?: string
}

export default function GitStateBadge({ branch, gitState }: GitStateBadgeProps) {
  const state = deriveGitState(branch ?? '', gitState ?? '')
  if (state === 'none') return null
  const config = GIT_STATE_CONFIG[state]
  return (
    <span
      className={`text-xs font-mono select-none ${config.className}`}
      title={`${config.label}${branch ? ` (${branch})` : ''}`}
    >
      {config.icon}
    </span>
  )
}
