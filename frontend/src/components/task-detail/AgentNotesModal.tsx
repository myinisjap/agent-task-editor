import { useEffect } from 'react'

type Props = {
  notes: string
  onClose: () => void
}

// Full-screen modal for reading the task overview's agent notes without the
// max-h-60 scroll clamp used in the inline preview. Modeled on the modal
// pattern in board/NewTaskModal.tsx (backdrop click + explicit close button),
// plus Escape-to-close and background-scroll lock for a better mobile/
// keyboard experience.
export default function AgentNotesModal({ notes, onClose }: Props) {
  useEffect(() => {
    function handleKeyDown(e: KeyboardEvent) {
      if (e.key === 'Escape') onClose()
    }
    document.addEventListener('keydown', handleKeyDown)
    const previousOverflow = document.body.style.overflow
    document.body.style.overflow = 'hidden'
    return () => {
      document.removeEventListener('keydown', handleKeyDown)
      document.body.style.overflow = previousOverflow
    }
  }, [onClose])

  function handleBackdrop(e: React.MouseEvent) {
    if (e.target === e.currentTarget) onClose()
  }

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/60"
      onClick={handleBackdrop}
      role="dialog"
      aria-modal="true"
      aria-label="Agent Notes"
    >
      <div className="bg-slate-900 border border-slate-700 rounded-xl shadow-2xl w-full max-w-2xl mx-4 max-h-[90vh] flex flex-col">
        <div className="flex items-center justify-between px-5 py-4 border-b border-slate-700">
          <h2 className="text-sm font-semibold text-slate-100">Agent Notes</h2>
          <button
            type="button"
            onClick={onClose}
            className="text-slate-500 hover:text-slate-300 transition-colors text-lg leading-none"
            title="Close"
          >
            ×
          </button>
        </div>
        <div className="p-5 overflow-y-auto">
          <pre className="text-sm text-slate-300 whitespace-pre-wrap font-sans">{notes}</pre>
        </div>
      </div>
    </div>
  )
}
