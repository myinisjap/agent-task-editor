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
  pr_merged: { label: 'PR merged',  icon: '⬢',  className: 'text-purple-400' },
  pr_closed: { label: 'PR closed',  icon: '⊗',  className: 'text-red-400' },
}

interface GitStateBadgeProps {
  branch?: string
  gitState?: string
  /** GitHub's mergeability verdict for the task's PR (see Task.pr_mergeable). */
  prMergeable?: string
}

export default function GitStateBadge({ branch, gitState, prMergeable }: GitStateBadgeProps) {
  const state = deriveGitState(branch ?? '', gitState ?? '')
  if (state === 'none') return null
  const config = GIT_STATE_CONFIG[state]
  const detail = `${config.label}${branch ? ` (${branch})` : ''}`
  const icon = (
    <span
      className={`text-xs font-mono select-none ${config.className}`}
      title={detail}
      aria-label={detail}
      role="img"
      tabIndex={0}
    >
      {config.icon}
    </span>
  )
  // Only an open PR can be usefully un-conflicted; a merged/closed one keeps
  // whatever verdict GitHub last reported, which isn't worth flagging.
  if (state !== 'pr_open' || prMergeable !== 'conflicting') return icon
  return (
    <span className="inline-flex items-center gap-1">
      {icon}
      <span
        className="text-xs font-mono select-none text-red-400"
        title="PR conflicts with its base branch"
        aria-label="merge conflict"
      >
        ⚠
      </span>
    </span>
  )
}
