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

describe('PageFrame description/fill/headerClassName', () => {
  it('renders description under the header', () => {
    wrap(<PageFrame title="T" description="About this page">x</PageFrame>)
    expect(screen.getByText('About this page')).toBeInTheDocument()
    expect(screen.getByRole('heading', { name: 'T' })).toHaveTextContent('T')
  })
  it('fill makes the root a full-height flex column', () => {
    wrap(<PageFrame title="T" fill testId="fl">x</PageFrame>)
    expect(screen.getByTestId('fl')).toHaveClass('h-full', 'min-h-0', 'flex', 'flex-col')
  })
  it('headerClassName overrides header margin', () => {
    wrap(<PageFrame title="T" headerClassName="mb-4" testId="hc">x</PageFrame>)
    expect(screen.getByRole('heading').parentElement).toHaveClass('mb-4')
    expect(screen.getByRole('heading').parentElement).not.toHaveClass('mb-6')
  })
})
