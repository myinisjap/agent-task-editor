import { describe, it, expect, vi, beforeEach } from 'vitest'

const list = vi.fn()
vi.mock('../api/client', () => ({ api: { workflows: { list: (...a: unknown[]) => list(...a) } } }))

import { useWorkflowStore } from './workflow'

const wf = (id: string, name: string, is_default: boolean) =>
  ({ id, name, description: '', labels: [], transitions: [], created_at: '', updated_at: '', is_default })

const PR = wf('pr', 'PR review', false)
const DEF = wf('def', 'Default', true)

beforeEach(() => {
  localStorage.clear()
  list.mockReset()
  list.mockResolvedValue([PR, DEF])
  useWorkflowStore.setState({ workflows: [], selectedId: null, loading: false })
})

describe('workflow store default selection', () => {
  it('opens on the server default, not list[0]', async () => {
    await useWorkflowStore.getState().fetch()
    expect(useWorkflowStore.getState().selectedId).toBe('def')
    expect(useWorkflowStore.getState().active()?.id).toBe('def')
    expect(localStorage.getItem('workflow.selectedId')).toBeNull()
  })

  it('keeps a valid explicit selection', async () => {
    useWorkflowStore.setState({ selectedId: 'pr' })
    await useWorkflowStore.getState().fetch()
    expect(useWorkflowStore.getState().active()?.id).toBe('pr')
  })

  it('falls back to default for a stale id and does not persist it', async () => {
    localStorage.setItem('workflow.selectedId', 'gone')
    useWorkflowStore.setState({ selectedId: 'gone' })
    await useWorkflowStore.getState().fetch()
    expect(useWorkflowStore.getState().selectedId).toBe('def')
    expect(localStorage.getItem('workflow.selectedId')).toBeNull()
  })

  it('setSelectedId persists', () => {
    useWorkflowStore.getState().setSelectedId('pr')
    expect(localStorage.getItem('workflow.selectedId')).toBe('pr')
  })
})
