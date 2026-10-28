import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import type { Task } from '../api/client'
import {
  computeColumnModes,
  columnWidthClass,
  loadExpandedTerminals,
  saveExpandedTerminals,
  groupTasksByRepo,
} from './boardLayout'

const col = (key: string, count: number, o: Partial<{ isTerminal: boolean; isStarting: boolean }> = {}) => ({
  key, count, isTerminal: false, isStarting: false, ...o,
})

describe('computeColumnModes', () => {
  it('starting column is always full, even empty', () => {
    expect(computeColumnModes([col('a', 0, { isStarting: true })], new Set()).get('a')).toBe('full')
  })
  it('empty non-terminal narrow, non-empty full', () => {
    const m = computeColumnModes([col('a', 0), col('b', 2)], new Set())
    expect(m.get('a')).toBe('narrow')
    expect(m.get('b')).toBe('full')
  })
  it('terminal collapsed by default, expanded when in set', () => {
    const cols = [col('d', 0, { isTerminal: true }), col('e', 4, { isTerminal: true })]
    const m = computeColumnModes(cols, new Set())
    expect(m.get('d')).toBe('collapsed')
    expect(m.get('e')).toBe('collapsed')
    expect(computeColumnModes(cols, new Set(['e'])).get('e')).toBe('full')
  })
  it('starting wins over terminal', () => {
    expect(computeColumnModes([col('a', 0, { isStarting: true, isTerminal: true })], new Set()).get('a')).toBe('full')
  })
})

describe('columnWidthClass', () => {
  it('maps modes', () => {
    expect(columnWidthClass('full', false)).toContain('15rem')
    expect(columnWidthClass('narrow', false)).toBe('w-10')
    expect(columnWidthClass('collapsed', false)).toBe('w-10')
    expect(columnWidthClass('narrow', true)).toContain('15rem')
    expect(columnWidthClass('collapsed', true)).toContain('15rem')
  })
})

describe('expanded terminal storage', () => {
  beforeEach(() => localStorage.clear())
  afterEach(() => vi.restoreAllMocks())
  it('round-trips and is per workflow', () => {
    saveExpandedTerminals('A', new Set(['done']))
    expect([...loadExpandedTerminals('A')]).toEqual(['done'])
    expect(loadExpandedTerminals('B').size).toBe(0)
  })
  it('handles missing id, corrupt and non-array data', () => {
    expect(loadExpandedTerminals(undefined).size).toBe(0)
    localStorage.setItem('board.expandedTerminals.A', '{nope')
    expect(loadExpandedTerminals('A').size).toBe(0)
    localStorage.setItem('board.expandedTerminals.A', '{"a":1}')
    expect(loadExpandedTerminals('A').size).toBe(0)
  })
  it('handles throwing storage', () => {
    vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => { throw new Error('x') })
    expect(loadExpandedTerminals('A').size).toBe(0)
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => { throw new Error('x') })
    expect(() => saveExpandedTerminals('A', new Set(['a']))).not.toThrow()
  })
})

describe('groupTasksByRepo', () => {
  const t = (id: string, repo?: string) => ({ id, repo_id: repo }) as unknown as Task
  it('empty → []', () => expect(groupTasksByRepo([], [])).toEqual([]))
  it('sorts by name and puts unknown last', () => {
    const lanes = groupTasksByRepo(
      [t('1', 'r2'), t('2', 'r1'), t('3'), t('4', 'zz'), t('5', 'r2')],
      [{ id: 'r1', name: 'zeta' }, { id: 'r2', name: 'Alpha' }, { id: 'r3', name: 'empty' }],
    )
    expect(lanes.map((l) => l.name)).toEqual(['Alpha', 'zeta', 'Unknown repo'])
    expect(lanes[0].tasks.map((x) => x.id)).toEqual(['1', '5'])
    expect(lanes[2].repoId).toBe('')
    expect(lanes[2].tasks.map((x) => x.id)).toEqual(['3', '4'])
  })
})
