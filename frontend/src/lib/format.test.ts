import { describe, it, expect } from 'vitest'
import { formatDuration, formatRelativeCountdown } from './format'

describe('formatDuration', () => {
  it('renders an em dash for zero/negative/falsy input', () => {
    expect(formatDuration(0)).toBe('—')
    expect(formatDuration(-5)).toBe('—')
  })

  it('renders seconds under a minute', () => {
    expect(formatDuration(42)).toBe('42s')
  })

  it('renders minutes and seconds over a minute', () => {
    expect(formatDuration(125)).toBe('2m 5s')
  })
})

describe('formatRelativeCountdown', () => {
  const now = new Date('2026-08-03T12:00:00Z')

  it('returns "now" for a timestamp at or before now', () => {
    expect(formatRelativeCountdown('2026-08-03T12:00:00Z', now)).toBe('now')
    expect(formatRelativeCountdown('2026-08-03T11:59:00Z', now)).toBe('now')
  })

  it('renders seconds under a minute', () => {
    expect(formatRelativeCountdown('2026-08-03T12:00:30Z', now)).toBe('in 30s')
  })

  it('renders minutes under an hour', () => {
    expect(formatRelativeCountdown('2026-08-03T12:05:00Z', now)).toBe('in 5m')
  })

  it('renders hours under a day', () => {
    expect(formatRelativeCountdown('2026-08-03T15:00:00Z', now)).toBe('in 3h')
  })

  it('renders days at or beyond a day', () => {
    expect(formatRelativeCountdown('2026-08-05T12:00:00Z', now)).toBe('in 2d')
  })
})
