import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import { MemoryRouter, useLocation } from 'react-router-dom'
import { useReviewSelection } from './useReviewSelection'
import type { InboxEntry } from './inbox'

const e = (id: string) => ({ kind: 'needs_me', item: { task_id: id, task_title: id, reason: 'human_gate', since: 'x' } }) as unknown as InboxEntry

function Probe({ flat, loaded }: { flat: InboxEntry[]; loaded: boolean }) {
  const sel = useReviewSelection(flat, loaded)
  const loc = useLocation()
  return <div data-testid="p">{`${sel.isOpen}|${sel.selectedKey}|${loc.search}`}</div>
}
const at = (url: string, flat: InboxEntry[], loaded = true) =>
  render(<MemoryRouter initialEntries={[url]}><Probe flat={flat} loaded={loaded} /></MemoryRouter>)

describe('useReviewSelection', () => {
  it('opens on a matching key', () => {
    at('/?task=b', [e('a'), e('b')])
    expect(screen.getByTestId('p')).toHaveTextContent('true|b|?task=b')
  })
  it('review=1 becomes the first entry', async () => {
    at('/?review=1', [e('a'), e('b')])
    expect(await screen.findByText('true|a|?task=a')).toBeInTheDocument()
  })
  it('review=1 on an empty queue just drops the param', async () => {
    at('/?review=1', [])
    expect(await screen.findByText('false|undefined|')).toBeInTheDocument()
  })
  it('drops a missing task once loaded, not before', async () => {
    const { unmount } = at('/?task=zzz', [e('a')], false)
    expect(screen.getByTestId('p')).toHaveTextContent('false|undefined|?task=zzz')
    unmount()
    at('/?task=zzz', [e('a')], true)
    expect(await screen.findByText('false|undefined|')).toBeInTheDocument()
  })
})
