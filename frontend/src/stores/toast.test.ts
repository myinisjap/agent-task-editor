import { describe, it, expect, beforeEach } from 'vitest'
import { useToastStore, showToast, showUndoToast } from './toast'

beforeEach(() => useToastStore.setState({ toast: null }))

describe('toast store', () => {
  it('dismiss(oldId) does not clear a newer toast', () => {
    showToast('one')
    const first = useToastStore.getState().toast!.id
    showToast('two')
    useToastStore.getState().dismiss(first)
    expect(useToastStore.getState().toast?.message).toBe('two')
  })

  it('Undo runs the inverse and a failure becomes an error toast', async () => {
    let called = false
    showUndoToast('Archived', async () => {
      called = true
      throw new Error('nope')
    })
    useToastStore.getState().toast!.action!.onClick()
    await new Promise((r) => setTimeout(r, 0))
    expect(called).toBe(true)
    const t = useToastStore.getState().toast!
    expect(t.variant).toBe('error')
    expect(t.message).toContain('nope')
  })
})
