export type DiffLine = {
  type: 'context' | 'add' | 'remove' | 'header'
  content: string
  oldNo: number | null
  newNo: number | null
}

export type Hunk = {
  header: string
  lines: DiffLine[]
}

export type FileDiff = {
  oldPath: string
  newPath: string
  isNew: boolean
  isDeleted: boolean
  hunks: Hunk[]
}

export function parseDiff(raw: string): FileDiff[] {
  const files: FileDiff[] = []
  if (!raw.trim()) return files

  const lines = raw.split('\n')
  let i = 0

  while (i < lines.length) {
    const line = lines[i]
    if (!line.startsWith('diff --git ')) {
      i++
      continue
    }

    // Parse file header
    let oldPath = ''
    let newPath = ''
    let isNew = false
    let isDeleted = false

    i++
    while (i < lines.length && !lines[i].startsWith('@@') && !lines[i].startsWith('diff --git ')) {
      const l = lines[i]
      if (l.startsWith('--- ')) oldPath = l.slice(4).replace(/^a\//, '')
      if (l.startsWith('+++ ')) newPath = l.slice(4).replace(/^b\//, '')
      if (l.startsWith('new file')) isNew = true
      if (l.startsWith('deleted file')) isDeleted = true
      i++
    }

    if (oldPath === '/dev/null') oldPath = newPath
    if (newPath === '/dev/null') newPath = oldPath

    const hunks: Hunk[] = []

    while (i < lines.length && !lines[i].startsWith('diff --git ')) {
      const hunkHeader = lines[i]
      if (!hunkHeader.startsWith('@@')) {
        i++
        continue
      }

      // Parse @@ -a,b +c,d @@ optional context
      const match = hunkHeader.match(/^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@/)
      let oldNo = match ? parseInt(match[1], 10) : 1
      let newNo = match ? parseInt(match[3], 10) : 1

      const hunkLines: DiffLine[] = [{
        type: 'header',
        content: hunkHeader,
        oldNo: null,
        newNo: null,
      }]
      i++

      while (i < lines.length && !lines[i].startsWith('@@') && !lines[i].startsWith('diff --git ')) {
        const dl = lines[i]
        if (dl.startsWith('+')) {
          hunkLines.push({ type: 'add', content: dl.slice(1), oldNo: null, newNo: newNo++ })
        } else if (dl.startsWith('-')) {
          hunkLines.push({ type: 'remove', content: dl.slice(1), oldNo: oldNo++, newNo: null })
        } else if (dl.startsWith('\\')) {
          // "\ No newline at end of file" — skip
        } else {
          hunkLines.push({ type: 'context', content: dl.slice(1), oldNo: oldNo++, newNo: newNo++ })
        }
        i++
      }

      hunks.push({ header: hunkHeader, lines: hunkLines })
    }

    files.push({ oldPath, newPath, isNew, isDeleted, hunks })
  }

  return files
}
