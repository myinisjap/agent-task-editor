// Minimal, hand-rolled parser scoped exactly to the workflow YAML schema
// produced/accepted by the backend (see
// backend/internal/api/handlers/workflow_yaml.go: yamlWorkflow / yamlLabel /
// yamlTransition, and docs/workflows.md).
//
// This is *not* a general-purpose YAML parser. It only understands:
//   - top-level `key: value` scalar lines (used for `name` / `description`)
//   - top-level `labels:` / `transitions:` keys followed by an indented list
//     of 2-space-indented `- key: value` blocks
//   - scalar values that are quoted strings ("..." or '...'), unquoted
//     strings, `true` / `false` booleans, or integers
//
// It deliberately does NOT support YAML anchors/aliases, multiline strings,
// flow-style collections (`[a, b]` / `{a: b}`), comments mid-value, or
// arbitrary nesting depth. The frontend has no YAML library as a real
// dependency (see package.json) and this parser only needs to extract the
// handful of fields used for client-side workflow validation — it is not a
// substitute for the backend's `gopkg.in/yaml.v3`-based parsing, which is
// still the source of truth when saving.

export type ParsedWorkflowLabel = {
  name: string
  sortOrder: number
  isTerminal: boolean
  agentIgnore: boolean
}

export type ParsedWorkflowTransition = {
  from: string
  to: string
}

export type ParsedWorkflow = {
  name: string
  labels: ParsedWorkflowLabel[]
  transitions: ParsedWorkflowTransition[]
}

/** Strip a trailing/inline `# comment` (outside of quotes) and trim whitespace. */
function stripComment(s: string): string {
  let inSingle = false
  let inDouble = false
  for (let i = 0; i < s.length; i++) {
    const c = s[i]
    if (c === "'" && !inDouble) inSingle = !inSingle
    else if (c === '"' && !inSingle) inDouble = !inDouble
    else if (c === '#' && !inSingle && !inDouble) {
      // Only treat as a comment if preceded by whitespace or start-of-string,
      // matching common YAML convention (avoids stripping '#' inside bare
      // scalars like hex colors that aren't quoted).
      if (i === 0 || /\s/.test(s[i - 1])) return s.slice(0, i)
    }
  }
  return s
}

/** Parse a scalar YAML value into a JS string/boolean/number. */
function parseScalar(raw: string): string | boolean | number {
  const v = raw.trim()
  if (v === 'true') return true
  if (v === 'false') return false
  if (/^-?\d+$/.test(v)) return parseInt(v, 10)
  if (v.length >= 2 && v.startsWith('"') && v.endsWith('"')) {
    return v.slice(1, -1)
  }
  if (v.length >= 2 && v.startsWith("'") && v.endsWith("'")) {
    return v.slice(1, -1)
  }
  return v
}

/** Count leading spaces (indentation) of a line. */
function indentOf(line: string): number {
  const m = /^ */.exec(line)
  return m ? m[0].length : 0
}

type RawEntry = Record<string, string | boolean | number>

/**
 * Parse a block of lines representing a YAML list of maps, e.g.:
 *   - name: foo
 *     sort_order: 0
 *   - name: bar
 *     sort_order: 1
 * Each line must be indented at least `baseIndent`. The first line of each
 * item starts with `- ` at `baseIndent`; subsequent `key: value` lines for
 * that item are indented further (or line up after the `- `).
 */
function parseListOfMaps(lines: string[], baseIndent: number): RawEntry[] {
  const entries: RawEntry[] = []
  let current: RawEntry | null = null

  for (const raw of lines) {
    if (!raw.trim()) continue
    const cleaned = stripComment(raw)
    if (!cleaned.trim()) continue

    const indent = indentOf(cleaned)
    if (indent < baseIndent) continue // shouldn't happen; caller slices correctly

    const content = cleaned.slice(indent)

    if (content.startsWith('- ')) {
      // New list item begins.
      current = {}
      entries.push(current)
      const rest = content.slice(2)
      const colonIdx = rest.indexOf(':')
      if (colonIdx === -1) {
        throw new Error(`Could not parse YAML: expected "key: value" after "- " in line: "${raw}"`)
      }
      const key = rest.slice(0, colonIdx).trim()
      const value = rest.slice(colonIdx + 1)
      current[key] = parseScalar(value)
    } else if (content.startsWith('-') && content.trim() === '-') {
      // Bare "-" with value on next line(s) is not supported by this
      // simplified parser; treat as a new (empty) item.
      current = {}
      entries.push(current)
    } else {
      const colonIdx = content.indexOf(':')
      if (colonIdx === -1) {
        throw new Error(`Could not parse YAML: expected "key: value" in line: "${raw}"`)
      }
      if (!current) {
        throw new Error(`Could not parse YAML: found list item field before any "- " entry: "${raw}"`)
      }
      const key = content.slice(0, colonIdx).trim()
      const value = content.slice(colonIdx + 1)
      current[key] = parseScalar(value)
    }
  }

  return entries
}

export function parseWorkflowYaml(yaml: string): ParsedWorkflow {
  if (!yaml || !yaml.trim()) {
    throw new Error('Could not parse YAML: input is empty')
  }

  const allLines = yaml.replace(/\r\n/g, '\n').split('\n')

  let name = ''
  const labels: ParsedWorkflowLabel[] = []
  const transitions: ParsedWorkflowTransition[] = []

  let i = 0
  while (i < allLines.length) {
    const raw = allLines[i]
    const cleaned = stripComment(raw)
    if (!cleaned.trim()) {
      i++
      continue
    }

    const indent = indentOf(cleaned)
    if (indent !== 0) {
      // Skip stray indented content not under a recognized top-level key
      // (shouldn't normally happen if input is well-formed).
      i++
      continue
    }

    const content = cleaned.slice(indent)
    const colonIdx = content.indexOf(':')
    if (colonIdx === -1) {
      throw new Error(`Could not parse YAML: expected "key: value" at top level in line: "${raw}"`)
    }
    const key = content.slice(0, colonIdx).trim()
    const valuePart = content.slice(colonIdx + 1).trim()

    if (key === 'name') {
      name = String(parseScalar(valuePart))
      i++
      continue
    }

    if (key === 'description') {
      // Not needed for validation; skip.
      i++
      continue
    }

    if (key === 'labels' || key === 'transitions') {
      // Collect all following lines that are indented deeper than this key
      // (i.e. belong to this block), until we hit another top-level key.
      const blockLines: string[] = []
      let j = i + 1
      while (j < allLines.length) {
        const l = allLines[j]
        const c = stripComment(l)
        if (!c.trim()) {
          j++
          continue
        }
        if (indentOf(c) === 0) break
        blockLines.push(l)
        j++
      }

      const items = parseListOfMaps(blockLines, indentOf(blockLines[0] ?? ''))

      if (key === 'labels') {
        for (const item of items) {
          const lname = item.name
          if (typeof lname !== 'string' || !lname) {
            throw new Error('Could not parse YAML: a label entry is missing a "name" field')
          }
          labels.push({
            name: lname,
            sortOrder: typeof item.sort_order === 'number' ? item.sort_order : 0,
            isTerminal: item.is_terminal === true,
            agentIgnore: item.agent_ignore === true,
          })
        }
      } else {
        for (const item of items) {
          const from = item.from
          const to = item.to
          if (typeof from !== 'string' || !from || typeof to !== 'string' || !to) {
            throw new Error('Could not parse YAML: a transition entry is missing "from" or "to"')
          }
          transitions.push({ from, to })
        }
      }

      i = j
      continue
    }

    // Unknown top-level key — ignore (forward-compatible with fields this
    // validator doesn't need).
    i++
  }

  if (!name) {
    throw new Error('Could not parse YAML: missing required "name" field')
  }

  return { name, labels, transitions }
}
