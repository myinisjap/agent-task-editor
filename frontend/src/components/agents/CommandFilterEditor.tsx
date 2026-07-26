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
          {allowlistCap.support !== 'full' && allowlistCap.note}
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
          {denylistCap.support !== 'full' && denylistCap.note}
        </p>
      </Field>
    </>
  )
}
