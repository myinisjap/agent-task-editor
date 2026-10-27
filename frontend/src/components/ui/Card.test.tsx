import { describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen } from '@testing-library/react'
import Card from './Card'

describe('Card', () => {
  it('renders the requested element with padding', () => {
    render(<Card as="section" padding="md" data-testid="c">hi</Card>)
    const el = screen.getByTestId('c')
    expect(el.tagName).toBe('SECTION')
    expect(el).toHaveClass('bg-surface-raised', 'p-4')
  })
  it('selected and interactive styles', () => {
    render(<Card padding="sm" selected interactive data-testid="c" />)
    expect(screen.getByTestId('c')).toHaveClass('ring-accent', 'border-accent', 'cursor-pointer', 'p-3')
  })
  it('passes rest props through', () => {
    const onClick = vi.fn()
    render(<Card data-testid="c" onClick={onClick} aria-current="true" />)
    fireEvent.click(screen.getByTestId('c'))
    expect(onClick).toHaveBeenCalled()
    expect(screen.getByTestId('c')).toHaveAttribute('aria-current', 'true')
  })
})
