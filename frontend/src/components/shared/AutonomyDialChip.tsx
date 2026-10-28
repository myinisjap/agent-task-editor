import { Flag, Hand, Zap, type LucideIcon } from 'lucide-react'
import { Badge, Icon } from '../ui'
import { DIAL_NAME, DIAL_TONE, type DialMode } from '../../lib/autonomyDial'

const ICON: Record<DialMode, LucideIcon> = { ask: Hand, on_flags: Flag, auto: Zap }

/** The gate's autonomy mode as a toned chip: Always ask / Ask on flags / Auto. */
export default function AutonomyDialChip({ mode, size = 'sm' }: { mode: DialMode; size?: 'sm' | 'xs' }) {
  return (
    <Badge tone={DIAL_TONE[mode]} size={size}>
      <Icon icon={ICON[mode]} size="xs" />
      {DIAL_NAME[mode]}
    </Badge>
  )
}
