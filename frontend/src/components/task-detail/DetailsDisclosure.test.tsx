import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import DetailsDisclosure, { DETAILS_OPEN_KEY } from './DetailsDisclosure'

describe('DetailsDisclosure', () => {
  beforeEach(() => localStorage.clear())
  afterEach(() => vi.restoreAllMocks())

  it('is collapsed by default', () => {
    render(<DetailsDisclosure><p>inner</p></DetailsDisclosure>)
    expect(screen.queryByText('inner')).toBeNull()
    expect(screen.getByRole('button', { name: /details/i })).toHaveAttribute('aria-expanded', 'false')
  })

  it('opens on click and remembers it', () => {
    render(<DetailsDisclosure><p>inner</p></DetailsDisclosure>)
    fireEvent.click(screen.getByRole('button', { name: /details/i }))
    expect(screen.getByText('inner')).toBeInTheDocument()
    expect(localStorage.getItem(DETAILS_OPEN_KEY)).toBe('1')
  })

  it('mounts open when stored', () => {
    localStorage.setItem(DETAILS_OPEN_KEY, '1')
    render(<DetailsDisclosure><p>inner</p></DetailsDisclosure>)
    expect(screen.getByText('inner')).toBeInTheDocument()
  })

  it('survives storage errors', () => {
    vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => { throw new Error('x') })
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => { throw new Error('x') })
    render(<DetailsDisclosure><p>inner</p></DetailsDisclosure>)
    expect(screen.queryByText('inner')).toBeNull()
    fireEvent.click(screen.getByRole('button', { name: /details/i }))
    expect(screen.getByText('inner')).toBeInTheDocument()
  })
})
