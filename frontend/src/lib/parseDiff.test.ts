import { describe, it, expect } from 'vitest'
import sampleDiff from './__fixtures__/sample.diff?raw'
import { parseDiff } from './parseDiff'

describe('parseDiff', () => {
  it('returns no files for empty or whitespace-only input', () => {
    expect(parseDiff('')).toEqual([])
    expect(parseDiff('   \n  \n')).toEqual([])
  })

  it('ignores content with no "diff --git" header', () => {
    expect(parseDiff('just some text\nwith no diff header')).toEqual([])
  })

  it('parses a real multi-file git diff fixture', () => {
    const files = parseDiff(sampleDiff)
    expect(files.map((f) => f.newPath)).toEqual([
      'frontend/src/lib/parseDiff.ts',
      'frontend/src/lib/README.md',
      'frontend/src/lib/legacy.ts',
    ])
  })

  it('strips a/ and b/ prefixes from paths', () => {
    const [file] = parseDiff(sampleDiff)
    expect(file.oldPath).toBe('frontend/src/lib/parseDiff.ts')
    expect(file.newPath).toBe('frontend/src/lib/parseDiff.ts')
  })

  it('detects a new file and resolves /dev/null oldPath to the new path', () => {
    const readme = parseDiff(sampleDiff).find((f) => f.newPath.endsWith('README.md'))!
    expect(readme.isNew).toBe(true)
    expect(readme.isDeleted).toBe(false)
    // oldPath was /dev/null and should fall back to newPath
    expect(readme.oldPath).toBe('frontend/src/lib/README.md')
  })

  it('detects a deleted file and resolves /dev/null newPath to the old path', () => {
    const legacy = parseDiff(sampleDiff).find((f) => f.oldPath.endsWith('legacy.ts'))!
    expect(legacy.isDeleted).toBe(true)
    expect(legacy.isNew).toBe(false)
    expect(legacy.newPath).toBe('frontend/src/lib/legacy.ts')
  })

  it('captures multiple hunks per file', () => {
    const [file] = parseDiff(sampleDiff)
    expect(file.hunks.length).toBe(2)
    expect(file.hunks[0].header).toMatch(/^@@ -21,7 \+21,7 @@/)
  })

  it('assigns correct line numbers for context, add, and remove lines', () => {
    const [file] = parseDiff(sampleDiff)
    const firstHunk = file.hunks[0]

    // First entry is the hunk header with null line numbers.
    expect(firstHunk.lines[0]).toMatchObject({ type: 'header', oldNo: null, newNo: null })

    const removed = firstHunk.lines.find((l) => l.type === 'remove')!
    expect(removed.content).toBe("  const lines = raw.split('\\n')")
    expect(removed.oldNo).toBe(24)
    expect(removed.newNo).toBeNull()

    const added = firstHunk.lines.find((l) => l.type === 'add')!
    expect(added.content).toBe("  const lines = raw.split(/\\r?\\n/)")
    expect(added.newNo).toBe(24)
    expect(added.oldNo).toBeNull()

    const context = firstHunk.lines.find((l) => l.type === 'context')!
    expect(context.oldNo).not.toBeNull()
    expect(context.newNo).not.toBeNull()
  })

  it('defaults hunk line numbers to 1 when the @@ header is malformed', () => {
    const diff = [
      'diff --git a/x.txt b/x.txt',
      '--- a/x.txt',
      '+++ b/x.txt',
      '@@ malformed header @@',
      '+added line',
      ' context line',
    ].join('\n')
    const [file] = parseDiff(diff)
    const added = file.hunks[0].lines.find((l) => l.type === 'add')!
    expect(added.newNo).toBe(1)
    const context = file.hunks[0].lines.find((l) => l.type === 'context')!
    expect(context.oldNo).toBe(1)
    expect(context.newNo).toBe(2)
  })

  it('skips the "\\ No newline at end of file" marker', () => {
    const readme = parseDiff(sampleDiff).find((f) => f.newPath.endsWith('README.md'))!
    const marker = readme.hunks[0].lines.find((l) => l.content.includes('No newline'))
    expect(marker).toBeUndefined()
  })
})
