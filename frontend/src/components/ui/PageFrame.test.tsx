import { describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import PageFrame from './PageFrame'

const wrap = (ui: React.ReactNode, path = '/') => render(<MemoryRouter initialEntries={[path]}>{ui}</MemoryRouter>)

describe('PageFrame', () => {
  it('renders exact h1 title, adornment and actions', () => {
    wrap(<PageFrame title="Inbox" titleAdornment={<span>(3)</span>} actions={<button>Act</button>} testId="pg">body</PageFrame>)
    expect(screen.getByRole('heading', { name: 'Inbox' })).toBeInTheDocument()
    expect(screen.getByText('(3)')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Act' })).toBeInTheDocument()
    expect(screen.getByTestId('pg')).toHaveClass('max-w-5xl')
  })
  it('help button calls onClick', () => {
    const onClick = vi.fn()
    wrap(<PageFrame title="T" help={{ onClick, title: 'About' }}>x</PageFrame>)
    fireEvent.click(screen.getByRole('button', { name: 'About' }))
    expect(onClick).toHaveBeenCalled()
  })
  it('renders tabs with active state', () => {
    wrap(<PageFrame title="T" tabs={[{ to: '/a', label: 'A', badge: <i>1</i> }, { to: '/b', label: 'B' }]}>x</PageFrame>, '/a')
    expect(screen.getByRole('link', { name: /^A/  })).toHaveClass('text-accent-fg')
    expect(screen.getByRole('link', { name: 'B' })).toHaveClass('text-fg-muted')
  })
  it('applies width classes', () => {
    wrap(<><PageFrame title="W" width="wide" testId="w">x</PageFrame><PageFrame title="F" width="full" testId="f">x</PageFrame></>)
    expect(screen.getByTestId('w')).toHaveClass('max-w-[1600px]')
    expect(screen.getByTestId('f').className).not.toMatch(/max-w-/)
  })
})
