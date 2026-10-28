import { describe, expect, it } from 'vitest'
import { render, screen } from '@testing-library/react'
import Badge from './Badge'

describe('Badge', () => {
  it('neutral by default', () => {
    render(<Badge>n</Badge>)
    expect(screen.getByText('n')).toHaveClass('bg-surface-sunken', 'text-fg-muted')
  })
  it.each(['success', 'warning', 'danger', 'info'] as const)('%s tone is soft', (tone) => {
    render(<Badge tone={tone}>t</Badge>)
    expect(screen.getByText('t')).toHaveClass(`bg-${tone}-soft`, `text-${tone}`)
  })
  it('accent soft and solid', () => {
    render(<><Badge tone="accent">a</Badge><Badge tone="accent" variant="solid" size="xs">b</Badge></>)
    expect(screen.getByText('a')).toHaveClass('text-accent-fg')
    expect(screen.getByText('b')).toHaveClass('bg-accent', 'text-on-accent', 'text-[10px]')
  })
  it('danger solid and unsupported solid fallback', () => {
    render(<><Badge tone="danger" variant="solid">d</Badge><Badge tone="info" variant="solid" className="x">i</Badge></>)
    expect(screen.getByText('d')).toHaveClass('bg-danger-solid', 'text-on-accent')
    expect(screen.getByText('i')).toHaveClass('bg-info-soft', 'x')
  })
})
