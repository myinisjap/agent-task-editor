import Field from './Field'
import { getCapability } from '../../lib/providerCapabilities'

export default function CommandFilterEditor({ provider, allowlist, denylist, onAllowlistChange, onDenylistChange }: {
  provider: string
  allowlist: string
  denylist: string
  onAllowlistChange: (v: string) => void
  onDenylistChange: (v: string) => void
}) {
  const allowlistCap = getCapability(provider, 'commandAllowlist')
  const denylistCap = getCapability(provider, 'commandDenylist')

  return (
    <>
      <p className="sm:col-span-2 rounded border border-amber-800/60 bg-amber-950/30 px-3 py-2 text-xs text-amber-300/90">
        These lists are guardrails, not a sandbox. They match command prefixes, so an agent can get
        around them with <code className="font-mono">sh -c</code>, <code className="font-mono">env</code>,
        an interpreter or a script. Don't rely on them to contain an untrusted or prompt-injected agent.
      </p>

      <Field label="Command allowlist (JSON array of glob patterns)" className="sm:col-span-2">
        <textarea
          value={allowlist}
          onChange={(e) => onAllowlistChange(e.target.value)}
          rows={2}
          className="input resize-none font-mono text-xs"
          placeholder='["git *", "npm test", "go *"]'
        />
        <p className="mt-1 text-xs text-slate-500">
          If non-empty, only run_bash/Bash commands matching a pattern here are allowed. "*" is a wildcard.
          Best-effort string matching; an agent can bypass it.{' '}
          {allowlistCap.support !== 'full' && allowlistCap.note}
        </p>
      </Field>

      <Field label="Command denylist (JSON array of glob patterns)" className="sm:col-span-2">
        <textarea
          value={denylist}
          onChange={(e) => onDenylistChange(e.target.value)}
          rows={2}
          className="input resize-none font-mono text-xs"
          placeholder='["rm -rf *", "curl *", "sudo *"]'
        />
        <p className="mt-1 text-xs text-slate-500">
          Commands matching any pattern here are always denied, checked before the allowlist.{' '}
          {denylistCap.support !== 'full' && denylistCap.note}
        </p>
      </Field>
    </>
  )
}
