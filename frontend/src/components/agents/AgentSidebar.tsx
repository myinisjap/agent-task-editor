import type { AgentConfig } from '../../api/client'
import { TEMPLATES } from '../../lib/agentTemplates'

export default function AgentSidebar({
  agents,
  selected,
  onSelect,
  onNew,
  multiMode,
  setMultiMode,
  multiSelected,
  setMultiSelected,
  onBulkToggle,
  bulkSaving,
  showTemplates,
  setShowTemplates,
  creatingTemplate,
  onApplyTemplate,
  isOpen,
  onClose,
}: {
  agents: AgentConfig[]
  selected: AgentConfig | null
  onSelect: (agent: AgentConfig) => void
  onNew: () => void
  multiMode: boolean
  setMultiMode: (updater: (v: boolean) => boolean) => void
  multiSelected: Set<string>
  setMultiSelected: (updater: Set<string> | ((prev: Set<string>) => Set<string>)) => void
  onBulkToggle: (enable: boolean) => void
  bulkSaving: boolean
  showTemplates: boolean
  setShowTemplates: (updater: (v: boolean) => boolean) => void
  creatingTemplate: boolean
  onApplyTemplate: (t: typeof TEMPLATES[0]) => void
  isOpen: boolean
  onClose: () => void
}) {
  return (
    <>
      {/* Backdrop — only on mobile when drawer is open */}
      {isOpen && (
        <div
          className="fixed inset-0 bg-black/50 z-30 md:hidden"
          onClick={onClose}
        />
      )}

      <div
        className={`fixed inset-y-0 left-0 z-40 w-64 max-w-[80vw] bg-slate-950 border-r border-slate-800 overflow-y-auto flex flex-col transition-transform duration-200 ease-in-out
          md:static md:z-auto md:w-56 md:max-w-none md:translate-x-0
          ${isOpen ? 'translate-x-0' : '-translate-x-full md:translate-x-0'}`}
      >
      <div className="p-4 flex items-center justify-between border-b border-slate-800">
        <span className="text-sm font-medium text-slate-300">Agent Configs</span>
        <div className="flex items-center gap-1.5">
          <button
            onClick={() => { setMultiMode((v) => !v); setMultiSelected(new Set()) }}
            className="text-xs px-2 py-1 rounded bg-slate-700 hover:bg-slate-600 text-slate-300"
          >
            {multiMode ? 'Done' : 'Select'}
          </button>
          {!multiMode && (
            <button
              onClick={() => { onNew(); onClose() }}
              className="text-xs px-2 py-1 rounded bg-indigo-700 hover:bg-indigo-600 text-white"
            >
              + New
            </button>
          )}
          <button
            onClick={onClose}
            aria-label="Close configs"
            className="md:hidden text-slate-400 hover:text-slate-100 p-1 rounded"
          >
            ✕
          </button>
        </div>
      </div>

      {/* Templates section */}
      <div className="border-b border-slate-800">
        <button
          onClick={() => setShowTemplates((v) => !v)}
          className="w-full flex items-center justify-between px-4 py-2.5 text-xs font-medium text-slate-400 hover:text-slate-200 hover:bg-slate-800/50"
        >
          <span>Templates</span>
          <span className="text-slate-600">{showTemplates ? '▲' : '▼'}</span>
        </button>
        {showTemplates && (
          <div className="flex flex-col gap-0.5 px-2 pb-2">
            {TEMPLATES.map((t) => (
              <button
                key={t.name}
                onClick={() => onApplyTemplate(t)}
                disabled={creatingTemplate}
                className="text-left text-xs px-3 py-1.5 rounded text-indigo-400 hover:bg-slate-800 hover:text-indigo-300 disabled:opacity-50"
              >
                + {t.name}
                <span className="ml-1 text-slate-600">{JSON.parse(t.labels)[0] ?? ''}</span>
              </button>
            ))}
          </div>
        )}
      </div>

      {/* Agent list */}
      <div className="flex flex-col gap-0.5 p-2">
        {agents.map((a) => {
          const isChecked = multiSelected.has(a.id)
          const isDisabled = a.enabled === 0 || a.enabled === false
          return (
            <button
              key={a.id}
              onClick={() => {
                if (multiMode) {
                  setMultiSelected((prev) => {
                    const next = new Set(prev)
                    if (next.has(a.id)) next.delete(a.id); else next.add(a.id)
                    return next
                  })
                } else {
                  onSelect(a)
                  onClose()
                }
              }}
              className={`w-full text-left text-sm px-3 py-2 rounded flex items-start gap-2 ${
                !multiMode && selected?.id === a.id
                  ? 'bg-slate-700 text-slate-100'
                  : 'text-slate-400 hover:bg-slate-800 hover:text-slate-200'
              } ${!multiMode && isDisabled ? 'opacity-50' : ''} ${
                multiMode && isChecked ? 'ring-1 ring-indigo-500 bg-slate-800' : ''
              }`}
            >
              {multiMode && (
                <span className={`mt-0.5 w-3.5 h-3.5 rounded border shrink-0 flex items-center justify-center ${
                  isChecked ? 'bg-indigo-600 border-indigo-500' : 'border-slate-600'
                }`}>
                  {isChecked && <span className="text-white text-[10px] leading-none">✓</span>}
                </span>
              )}
              <span className="flex-1 min-w-0">
                <div className="truncate flex items-center gap-1.5">
                  {!multiMode && isDisabled && (
                    <span className="w-1.5 h-1.5 rounded-full bg-slate-600 shrink-0" />
                  )}
                  {multiMode && isDisabled && (
                    <span className="text-slate-600 text-[10px]">[off]</span>
                  )}
                  {a.name}
                </div>
                <div className="text-xs text-slate-500 mt-0.5">{a.provider}/{a.model.split('-').slice(0,2).join('-')}</div>
              </span>
            </button>
          )
        })}
        {agents.length === 0 && (
          <p className="text-xs text-slate-600 px-3 py-4">No agents configured</p>
        )}
      </div>

      {/* Bulk action bar — shown only in multi mode */}
      {multiMode && (
        <div className="mt-auto p-2 border-t border-slate-800 flex flex-col gap-1.5">
          <p className="text-xs text-slate-400 px-1">
            {multiSelected.size > 0 ? `${multiSelected.size} selected` : 'Tap agents to select'}
          </p>
          {multiSelected.size > 0 && (
            <>
              <button
                onClick={() => onBulkToggle(true)}
                disabled={bulkSaving}
                className="text-xs px-2 py-1.5 rounded bg-green-700 hover:bg-green-600 text-white disabled:opacity-50"
              >
                {bulkSaving ? 'Saving…' : 'Enable All'}
              </button>
              <button
                onClick={() => onBulkToggle(false)}
                disabled={bulkSaving}
                className="text-xs px-2 py-1.5 rounded bg-slate-700 hover:bg-slate-600 text-slate-300 disabled:opacity-50"
              >
                {bulkSaving ? 'Saving…' : 'Disable All'}
              </button>
              <button
                onClick={() => setMultiSelected(new Set(agents.map((a) => a.id)))}
                className="text-xs px-2 py-1.5 rounded text-slate-500 hover:text-slate-300"
              >
                Select All
              </button>
            </>
          )}
        </div>
      )}
      </div>
    </>
  )
}
