#!/usr/bin/env node
// Regenerates the "Capability Matrix" table in docs/agents.md from
// frontend/src/lib/providerCapabilities.ts — the single source of truth
// also consumed by AgentConfigForm, ProviderConfigForm, and
// CommandFilterEditor to surface capability gaps inline in the UI.
//
// Run via `npm run gen:capability-docs` (frontend/package.json), or
// directly: `node scripts/gen-capability-docs.mjs`.
//
// The generated block lives between these markers in docs/agents.md:
//   <!-- BEGIN capability-matrix (generated) -->
//   <!-- END capability-matrix (generated) -->
// Do not hand-edit the table inside those markers — edit
// frontend/src/lib/providerCapabilities.ts and re-run this script instead.

import { readFile, writeFile } from 'node:fs/promises'
import { fileURLToPath } from 'node:url'
import path from 'node:path'

const repoRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..')
const capabilitiesPath = path.join(repoRoot, 'frontend/src/lib/providerCapabilities.ts')
const docsPath = path.join(repoRoot, 'docs/agents.md')

const BEGIN = '<!-- BEGIN capability-matrix (generated) -->'
const END = '<!-- END capability-matrix (generated) -->'

const SUPPORT_ICON = { full: '✅', partial: '⚠️', none: '❌' }

// Rows to render, in table order: [Capability key, display label].
const ROWS = [
  ['taskEditorTools', 'Task-editor tools (6: transitions, complete, request-human, notes, store-info, resolve-comment)'],
  ['labelTransitions', 'Label / workflow transitions'],
  ['mcpServers', 'Plugins + user MCP servers'],
  ['commandAllowlist', 'Command allowlist'],
  ['commandDenylist', 'Command denylist'],
  ['costTracking', 'Cost & tokens'],
  ['costWatchdog', 'Mid-run cost kill switch'],
  ['imageAttachments', 'Image attachments'],
  ['maxTurns', '`max_turns`'],
  ['sessionResume', 'Session resume'],
  ['subtasks', 'Subtasks (`create_subtask`)'],
  ['effort', 'Effort (reasoning level)'],
]

function cell(entry) {
  if (!entry) return `${SUPPORT_ICON.none}`
  const icon = SUPPORT_ICON[entry.support] ?? SUPPORT_ICON.none
  return entry.note ? `${icon} ${entry.note}` : icon
}

async function main() {
  const { PROVIDER_CAPABILITIES, KNOWN_PROVIDERS, DEPRECATED_PROVIDERS } = await import(capabilitiesPath)

  const columnLabel = (p) => (DEPRECATED_PROVIDERS?.has(p) ? `\`${p}\` (deprecated)` : `\`${p}\``)
  const header = `| Capability | ${KNOWN_PROVIDERS.map(columnLabel).join(' | ')} |`
  const divider = `|---|${KNOWN_PROVIDERS.map(() => '---').join('|')}|`
  const rows = ROWS.map(([key, label]) => {
    const cells = KNOWN_PROVIDERS.map((p) => cell(PROVIDER_CAPABILITIES[p]?.[key]))
    return `| ${label} | ${cells.join(' | ')} |`
  })

  const table = [header, divider, ...rows].join('\n')
  const block = [
    BEGIN,
    '',
    '_Generated from `frontend/src/lib/providerCapabilities.ts` by `npm run gen:capability-docs` — do not hand-edit._',
    '',
    table,
    '',
    END,
  ].join('\n')

  const doc = await readFile(docsPath, 'utf8')
  const beginIdx = doc.indexOf(BEGIN)
  const endIdx = doc.indexOf(END)
  if (beginIdx === -1 || endIdx === -1) {
    throw new Error(`docs/agents.md is missing the ${BEGIN} / ${END} markers`)
  }
  const updated = doc.slice(0, beginIdx) + block + doc.slice(endIdx + END.length)

  if (updated === doc) {
    console.log('docs/agents.md capability matrix already up to date.')
    return
  }
  await writeFile(docsPath, updated)
  console.log('docs/agents.md capability matrix regenerated.')
}

main().catch((err) => {
  console.error(err)
  process.exit(1)
})
