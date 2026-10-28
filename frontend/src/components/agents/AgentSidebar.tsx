import { Check, ChevronDown, ChevronUp, X } from 'lucide-react'
import Icon from '../ui/Icon'
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
  templateError,
  bulkError,
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
  templateError?: string
  bulkError?: string
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
        className={`fixed inset-y-0 left-0 z-40 w-64 max-w-[80vw] bg-surface border-r border-border overflow-y-auto flex flex-col transition-transform duration-200 ease-in-out
          md:sticky md:top-0 md:self-start md:max-h-[calc(100vh-8rem)] md:z-auto md:w-56 md:shrink-0 md:max-w-none md:translate-x-0 md:rounded-lg
          ${isOpen ? 'translate-x-0' : '-translate-x-full md:translate-x-0'}`}
      >
      <div className="p-4 flex items-center justify-between border-b border-border">
        <span className="text-sm font-medium text-fg-muted">Agent Configs</span>
        <div className="flex items-center gap-1.5">
          <button
            onClick={() => { setMultiMode((v) => !v); setMultiSelected(new Set()) }}
            className="text-xs px-2 py-1 rounded bg-border hover:bg-slate-600 text-fg-muted"
          >
            {multiMode ? 'Done' : 'Select'}
          </button>
          {!multiMode && (
            <button
              onClick={() => { onNew(); onClose() }}
              className="text-xs px-2 py-1 rounded bg-accent hover:bg-accent/90 text-on-accent"
            >
              + New
            </button>
          )}
          <button
            onClick={onClose}
            aria-label="Close configs"
            className="md:hidden text-fg-muted hover:text-fg p-1 rounded"
          >
            <Icon icon={X} size="md" />
          </button>
        </div>
      </div>

      {/* Templates section */}
      <div className="border-b border-border">
        <button
          onClick={() => setShowTemplates((v) => !v)}
          className="w-full flex items-center justify-between px-4 py-2.5 text-xs font-medium text-fg-muted hover:text-fg hover:bg-surface-sunken/50"
        >
          <span>Templates</span>
          <span className="text-fg-subtle"><Icon icon={showTemplates ? ChevronUp : ChevronDown} size="sm" /></span>
        </button>
        {showTemplates && (
          <div className="flex flex-col gap-0.5 px-2 pb-2">
            {TEMPLATES.map((t) => (
              <button
                key={t.name}
                onClick={() => onApplyTemplate(t)}
                disabled={creatingTemplate}
                className="text-left text-xs px-3 py-1.5 rounded text-accent-fg hover:bg-surface-sunken hover:text-accent-fg disabled:opacity-50"
              >
                + {t.name}
                <span className="ml-1 text-fg-subtle">{JSON.parse(t.labels)[0] ?? ''}</span>
              </button>
            ))}
          </div>
        )}
        {templateError && <p role="alert" className="text-xs text-danger px-4 pb-2">{templateError}</p>}
      </div>

      {/* Agent list */}
      <div className="flex flex-col gap-0.5 p-2">
        {agents.map((a) => {
          const isChecked = multiSelected.has(a.id)
          const isDisabled = !a.enabled
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
                  ? 'bg-border text-fg'
                  : 'text-fg-muted hover:bg-surface-sunken hover:text-fg'
              } ${!multiMode && isDisabled ? 'opacity-50' : ''} ${
                multiMode && isChecked ? 'ring-1 ring-accent bg-surface-sunken' : ''
              }`}
            >
              {multiMode && (
                <span className={`mt-0.5 w-3.5 h-3.5 rounded border shrink-0 flex items-center justify-center ${
                  isChecked ? 'bg-accent border-accent' : 'border-border'
                }`}>
                  {isChecked && <Icon icon={Check} size="xs" className="text-on-accent" />}
                </span>
              )}
              <span className="flex-1 min-w-0">
                <div className="truncate flex items-center gap-1.5">
                  {!multiMode && isDisabled && (
                    <span className="w-1.5 h-1.5 rounded-full bg-fg-subtle shrink-0" />
                  )}
                  {multiMode && isDisabled && (
                    <span className="text-fg-subtle text-[10px]">[off]</span>
                  )}
                  {a.name}
                </div>
                {a.provider_config && (
                  <div className="text-xs text-fg-subtle mt-0.5">{a.provider_config.provider}{a.provider_config.model ? `/${a.provider_config.model}` : ''}</div>
                )}
              </span>
            </button>
          )
        })}
        {agents.length === 0 && (
          <p className="text-xs text-fg-subtle px-3 py-4">No agents configured</p>
        )}
      </div>

      {/* Bulk action bar — shown only in multi mode */}
      {multiMode && (
        <div className="mt-auto p-2 border-t border-border flex flex-col gap-1.5">
          <p className="text-xs text-fg-muted px-1">
            {multiSelected.size > 0 ? `${multiSelected.size} selected` : 'Tap agents to select'}
          </p>
          {bulkError && <p role="alert" className="text-xs text-danger px-1">{bulkError}</p>}
          {multiSelected.size > 0 && (
            <>
              <button
                onClick={() => onBulkToggle(true)}
                disabled={bulkSaving}
                className="text-xs px-2 py-1.5 rounded bg-success-soft hover:bg-success-soft/70 text-success disabled:opacity-50"
              >
                {bulkSaving ? 'Saving…' : 'Enable All'}
              </button>
              <button
                onClick={() => onBulkToggle(false)}
                disabled={bulkSaving}
                className="text-xs px-2 py-1.5 rounded bg-border hover:bg-slate-600 text-fg-muted disabled:opacity-50"
              >
                {bulkSaving ? 'Saving…' : 'Disable All'}
              </button>
              <button
                onClick={() => setMultiSelected(new Set(agents.map((a) => a.id)))}
                className="text-xs px-2 py-1.5 rounded text-fg-subtle hover:text-fg-muted"
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
