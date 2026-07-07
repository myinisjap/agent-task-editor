import Field from './Field'

export default function CommandFilterEditor({ provider, allowlist, denylist, onAllowlistChange, onDenylistChange }: {
  provider: string
  allowlist: string
  denylist: string
  onAllowlistChange: (v: string) => void
  onDenylistChange: (v: string) => void
}) {
  return (
    <>
      <Field label="Command allowlist (JSON array of glob patterns)" className="col-span-2">
        <textarea
          value={allowlist}
          onChange={(e) => onAllowlistChange(e.target.value)}
          rows={2}
          className="input resize-none font-mono text-xs"
          placeholder='["git *", "npm test", "go *"]'
        />
        <p className="mt-1 text-xs text-slate-500">
          If non-empty, only run_bash/Bash commands matching a pattern here are allowed. "*" is a wildcard.
          Best-effort string matching, not a sandbox.{' '}
          {provider === 'opencode' && 'Not enforced for the opencode provider.'}
          {provider === 'claude' &&
            'Not an effective restriction for the claude provider: the CLI only auto-approves matches, it does not block non-matching commands. Use the denylist below instead.'}
        </p>
      </Field>

      <Field label="Command denylist (JSON array of glob patterns)" className="col-span-2">
        <textarea
          value={denylist}
          onChange={(e) => onDenylistChange(e.target.value)}
          rows={2}
          className="input resize-none font-mono text-xs"
          placeholder='["rm -rf *", "curl *", "sudo *"]'
        />
        <p className="mt-1 text-xs text-slate-500">
          Commands matching any pattern here are always denied, checked before the allowlist.{' '}
          {provider === 'opencode' && 'Not enforced for the opencode provider.'}
          {provider === 'qwen_code' && 'Not enforced for the qwen_code provider (no confirmed CLI denylist flag).'}
        </p>
      </Field>
    </>
  )
}
