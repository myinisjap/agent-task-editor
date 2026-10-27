import { useState } from 'react'
import { useLocation, useNavigate } from 'react-router-dom'
import CommandPalette from './CommandPalette'
import ShortcutSheet from './ShortcutSheet'
import Toast from './Toast'
import NewTaskModal from '../board/NewTaskModal'
import { useCommandPaletteStore } from '../../stores/commandPalette'
import { useWorkflowStore } from '../../stores/workflow'
import { useMediaQuery } from '../../lib/useMediaQuery'
import { useGlobalShortcuts, type GlobalAction } from '../../lib/shortcuts'

const GO_TARGETS: Partial<Record<GlobalAction, string>> = {
  'go-inbox': '/inbox',
  'go-board': '/board',
  'go-pipeline': '/pipeline',
  'go-settings': '/settings',
}

/** Mounted once under the router: palette, shortcut sheet, global new-task modal and key handling. */
export default function CommandHost() {
  const navigate = useNavigate()
  const location = useLocation()
  const sheetOpen = useCommandPaletteStore((s) => s.sheetOpen)
  const newTaskOpen = useCommandPaletteStore((s) => s.newTaskOpen)
  const touchOnly = useMediaQuery('(hover: none) and (pointer: coarse)')
  const [error, setError] = useState<string | null>(null)

  useGlobalShortcuts(
    {
      onTogglePalette: () => useCommandPaletteStore.getState().toggle(),
      onAction: (a) => {
        const st = useCommandPaletteStore.getState()
        if (a === 'new-task') st.setNewTaskOpen(true)
        else if (a === 'help') st.setSheetOpen(true)
        else if (a === 'focus-search') {
          if (!location.pathname.startsWith('/board')) navigate('/board')
          st.requestBoardSearchFocus()
        } else {
          const to = GO_TARGETS[a]
          if (to) navigate(to)
        }
      },
    },
    { singleKeyEnabled: !touchOnly },
  )

  return (
    <>
      <CommandPalette onError={setError} />
      {sheetOpen && <ShortcutSheet onClose={() => useCommandPaletteStore.getState().setSheetOpen(false)} />}
      {newTaskOpen && (
        <NewTaskModal
          workflow={useWorkflowStore.getState().active()}
          onClose={() => useCommandPaletteStore.getState().setNewTaskOpen(false)}
        />
      )}
      {error && <Toast message={error} onDismiss={() => setError(null)} />}
    </>
  )
}
