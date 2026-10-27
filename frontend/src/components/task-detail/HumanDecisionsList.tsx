import { ChevronDown, ChevronRight } from 'lucide-react'
import Icon from '../ui/Icon'
import { humanizeLabelName } from '../../lib/labelDisplay'
import { useState } from 'react'
import type { TaskHumanDecision } from '../../api/client'

function Item({ d, onRemember }: { d: TaskHumanDecision; onRemember?: (text: string) => void }) {
  const [open, setOpen] = useState(false)
  const when = d.replied_at ? new Date(d.replied_at).toLocaleString() : ''
  return (
    <div className="text-xs px-2 py-1.5 rounded text-fg-muted flex flex-col gap-1">
      <div className="text-[11px] text-fg-subtle break-words">
        <span>{humanizeLabelName(d.label)}</span>
        {d.agent_name && <span> · {d.agent_name}</span>}
        {when && <span> · {when}</span>}
      </div>
      <button onClick={() => setOpen((o) => !o)} className="text-left text-fg-subtle hover:text-fg-muted">
        <Icon icon={open ? ChevronDown : ChevronRight} size="sm" className="inline -mt-0.5" /> question
      </button>
      {open && (
        <pre className="text-xs text-fg-muted bg-surface-sunken rounded p-2 whitespace-pre-wrap break-words max-h-48 overflow-y-auto font-sans">
          {d.question}
        </pre>
      )}
      <p className="text-fg whitespace-pre-wrap break-words">{d.reply}</p>
      {onRemember && (
        <button
          onClick={() => onRemember(d.reply ?? '')}
          aria-label="Remember for this repo"
          className="self-start text-blue-400 hover:text-blue-300 hover:underline"
        >
          Remember for this repo
        </button>
      )}
    </div>
  )
}

export default function HumanDecisionsList({
  decisions,
  onRemember,
}: {
  decisions: TaskHumanDecision[]
  onRemember?: (text: string) => void
}) {
  if (decisions.length === 0) return null
  return (
    <div>
      <p className="text-xs text-fg-subtle mb-2">Human decisions</p>
      <div className="flex flex-col gap-1">
        {decisions.map((d) => (
          <Item key={d.run_id} d={d} onRemember={onRemember} />
        ))}
      </div>
    </div>
  )
}
