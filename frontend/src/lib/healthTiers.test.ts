import { describe, it, expect, vi, afterEach } from 'vitest'
import type { ProviderCheck } from '../api/client'
import {
  healthTier,
  isSnoozable,
  visibleIssues,
  healthSummary,
  pausedSignature,
  readBannerDismissed,
  writeBannerDismissed,
  readSnoozed,
  writeSnoozed,
} from './healthTiers'

const c = (id: string, status: ProviderCheck['status'] = 'warn'): ProviderCheck =>
  ({ id, name: id, status, detail: '' }) as ProviderCheck

describe('healthTier', () => {
  it('maps ok to null', () => expect(healthTier(c('claude_cli', 'ok'))).toBeNull())
  it('maps providers/mcp/gh/api_auth to attention', () => {
    for (const id of ['claude_cli', 'codex_cli', 'mcp_sidecar', 'gh_auth', 'api_auth', 'removed_provider_x']) {
      expect(healthTier(c(id))).toBe('attention')
    }
    expect(healthTier(c('claude_cli', 'error'))).toBe('attention')
  })
  it('maps backups/db/update to advice', () => {
    for (const id of ['auto_backup', 'db_size', 'update_check', 'version']) {
      expect(healthTier(c(id))).toBe('advice')
    }
  })
  it('repo_base_dir: unset is advice, set-but-missing is attention', () => {
    expect(healthTier(c('repo_base_dir', 'warn'))).toBe('advice')
    expect(healthTier(c('repo_base_dir', 'error'))).toBe('attention')
  })
  it('defaults unknown or missing ids to attention', () => {
    expect(healthTier(c('brand_new'))).toBe('attention')
    expect(healthTier({ status: 'warn' } as ProviderCheck)).toBe('attention')
  })
  it('never lets api_auth be snoozed', () => {
    expect(isSnoozable(c('api_auth'))).toBe(false)
    expect(isSnoozable(c('auto_backup'))).toBe(true)
  })
})

describe('visibleIssues / healthSummary', () => {
  const checks = [c('mcp_sidecar'), c('api_auth'), c('auto_backup'), c('repo_base_dir'), c('gh_auth', 'ok')]
  it('drops snoozed advice only', () => {
    expect(visibleIssues(checks, []).length).toBe(4)
    const v = visibleIssues(checks, ['auto_backup', 'mcp_sidecar', 'api_auth'])
    expect(v.map((x) => x.id)).toEqual(['mcp_sidecar', 'api_auth', 'repo_base_dir'])
  })
  it('computes count and severity', () => {
    expect(healthSummary(checks, ['auto_backup', 'repo_base_dir'], [])).toEqual({ count: 2, severity: 'warn' })
    expect(healthSummary([c('db_size')], ['db_size'], [])).toEqual({ count: 0, severity: null })
    expect(healthSummary([c('claude_cli', 'error')], [], [])).toEqual({ count: 1, severity: 'error' })
    expect(healthSummary([], [], [{ provider: 'claude', reason: '', paused_at: 'x' }])).toEqual({
      count: 0,
      severity: 'error',
    })
  })
})

describe('pausedSignature', () => {
  const a = { provider: 'claude', reason: '', paused_at: '1' }
  const b = { provider: 'codex_cli', reason: '', paused_at: '2' }
  it('is order independent and changes with paused_at', () => {
    expect(pausedSignature([a, b])).toBe(pausedSignature([b, a]))
    expect(pausedSignature([a])).not.toBe(pausedSignature([{ ...a, paused_at: '3' }]))
  })
})

describe('storage helpers', () => {
  afterEach(() => {
    vi.restoreAllMocks()
    sessionStorage.clear()
    localStorage.clear()
  })
  it('round-trips', () => {
    writeBannerDismissed('sig')
    expect(readBannerDismissed()).toBe('sig')
    writeSnoozed(['a'])
    expect(readSnoozed()).toEqual(['a'])
  })
  it('ignores malformed snooze data', () => {
    localStorage.setItem('ate:health-snoozed', '{bad')
    expect(readSnoozed()).toEqual([])
    localStorage.setItem('ate:health-snoozed', '["a",1]')
    expect(readSnoozed()).toEqual(['a'])
  })
  it('survives throwing storage', () => {
    vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
      throw new Error('x')
    })
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
      throw new Error('x')
    })
    expect(readBannerDismissed()).toBeNull()
    expect(readSnoozed()).toEqual([])
    expect(() => writeBannerDismissed('s')).not.toThrow()
    expect(() => writeSnoozed(['a'])).not.toThrow()
  })
})
