import { describe, it, expect } from 'vitest'
import { describeSystemText, severityForSubtype } from './logEvents'
import { parseLogContent } from './parseAgentLog'

describe('describeSystemText', () => {
  it('treats the pid line as debug', () => {
    expect(describeSystemText('started claude pid=3548')).toEqual({ severity: 'debug', message: 'Started claude (pid 3548)' })
  })
  it('rewrites the new and legacy conflict lines without contradiction', () => {
    const legacy = describeSystemText('claude exited with error but had parsed outcome "success" — treating as failed')
    const next = describeSystemText('Run failed: claude exited with an error (it had reported "success")')
    for (const d of [legacy, next]) {
      expect(d.severity).toBe('error')
      expect(d.message).toBe('Run failed: claude exited with an error (it had reported "success")')
      expect(d.message).not.toContain('treating as failed')
    }
  })
  it('marks rate limits as warnings and unknown text as info', () => {
    expect(describeSystemText('rate limit resets at 5pm').severity).toBe('warning')
    expect(describeSystemText('something else').severity).toBe('info')
  })
})

describe('severityForSubtype', () => {
  it('maps known subtypes and defaults unknown to debug', () => {
    expect(severityForSubtype('init')).toBe('info')
    expect(severityForSubtype('ui_invalidate')).toBe('debug')
    expect(severityForSubtype('brand_new_thing')).toBe('debug')
    expect(severityForSubtype('task_notification', 'failed')).toBe('warning')
    expect(severityForSubtype('task_notification', 'completed')).toBe('info')
  })
})

describe('parseLogContent severity', () => {
  it('flags an errored result as error', () => {
    const p = parseLogContent('stdout', JSON.stringify({ type: 'result', is_error: true, result: 'Not logged in · Please run /login' }))
    expect(p).toMatchObject({ kind: 'system_event', severity: 'error', event: 'Run failed: Not logged in · Please run /login' })
  })
  it('flags session start as info and ui_invalidate as debug', () => {
    expect(parseLogContent('stdout', JSON.stringify({ type: 'system', subtype: 'init' }))).toMatchObject({ severity: 'info' })
    expect(parseLogContent('stdout', JSON.stringify({ type: 'system', subtype: 'ui_invalidate' }))).toMatchObject({ severity: 'debug' })
  })
})
