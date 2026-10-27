import { describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen } from '@testing-library/react'
import Button, { buttonClasses } from './Button'

describe('Button', () => {
  it.each([
    ['primary', 'bg-accent', 'text-on-accent'],
    ['secondary', 'bg-surface-sunken', 'border-border'],
    ['ghost', 'text-fg-muted', 'hover:bg-surface-sunken'],
    ['danger', 'bg-danger-solid', 'text-on-accent'],
  ] as const)('renders %s variant', (variant, a, b) => {
    render(<Button variant={variant}>Go</Button>)
    const btn = screen.getByRole('button', { name: 'Go' })
    expect(btn).toHaveClass(a)
    expect(btn).toHaveClass(b)
  })

  it('applies sizes and merges className', () => {
    render(<><Button size="sm" className="extra">S</Button><Button size="md">M</Button></>)
    expect(screen.getByText('S')).toHaveClass('text-xs', 'extra')
    expect(screen.getByText('M')).toHaveClass('text-sm')
  })

  it('defaults to type=button and allows submit', () => {
    render(<><Button>A</Button><Button type="submit">B</Button></>)
    expect(screen.getByText('A')).toHaveAttribute('type', 'button')
    expect(screen.getByText('B')).toHaveAttribute('type', 'submit')
  })

  it('does not fire onClick when disabled', () => {
    const onClick = vi.fn()
    render(<Button disabled onClick={onClick}>X</Button>)
    fireEvent.click(screen.getByText('X'))
    expect(onClick).not.toHaveBeenCalled()
  })

  it('loading disables, sets aria-busy, shows spinner and keeps label', () => {
    render(<Button loading>Save</Button>)
    const btn = screen.getByRole('button', { name: 'Save' })
    expect(btn).toBeDisabled()
    expect(btn).toHaveAttribute('aria-busy', 'true')
    expect(screen.getByTestId('button-spinner')).toHaveAttribute('aria-hidden', 'true')
  })

  it('buttonClasses builds link styles', () => {
    const c = buttonClasses({ variant: 'primary', size: 'sm', className: 'z' })
    expect(c).toContain('bg-accent')
    expect(c).toContain('text-xs')
    expect(c.endsWith('z')).toBe(true)
    expect(buttonClasses()).toContain('bg-surface-sunken')
  })
})
