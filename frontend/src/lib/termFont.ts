export const MIN_FONT = 9
export const MAX_FONT = 18

export function isNarrowScreen(): boolean {
  return window.innerWidth < 640
}

export function defaultFontSize(narrow: boolean): number {
  return narrow ? 11 : 13
}

export function clampFont(n: number): number {
  return Math.min(MAX_FONT, Math.max(MIN_FONT, Math.round(n)))
}

export function stepFont(size: number, dir: 1 | -1): number {
  return clampFont(size + dir)
}

function storageKey(narrow: boolean): string {
  return `chat.fontSize.${narrow ? 'narrow' : 'wide'}`
}

export function loadFontSize(narrow: boolean): number {
  try {
    const raw = localStorage.getItem(storageKey(narrow))
    if (raw !== null) {
      const n = Number(raw)
      if (Number.isFinite(n)) return clampFont(n)
    }
  } catch { /* storage unavailable */ }
  return defaultFontSize(narrow)
}

export function saveFontSize(narrow: boolean, size: number): void {
  try {
    localStorage.setItem(storageKey(narrow), String(clampFont(size)))
  } catch { /* storage unavailable */ }
}

// Heuristic: does the last non-empty terminal line look like a question awaiting
// a short reply? Deliberately conservative.
export function looksLikePrompt(line: string): boolean {
  const s = line.trim()
  if (!s) return false
  if (/[[(]\s*(y\/n|yes\/no)\s*[\])]\s*[:?]?$/i.test(s)) return true
  if (/^[❯>▶●›]?\s*\d+[.)]\s+\S/.test(s)) return true
  return false
}
