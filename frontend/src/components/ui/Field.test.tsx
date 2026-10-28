import { describe, expect, it } from 'vitest'
import { render, screen } from '@testing-library/react'
import Field from './Field'

describe('Field', () => {
  it('associates label with control', () => {
    render(<Field label="Name"><input /></Field>)
    expect(screen.getByLabelText('Name')).toBeInTheDocument()
  })
  it('links hint and error and flags invalid', () => {
    render(<Field label="Name" hint="Hint text" error="Bad"><input /></Field>)
    const input = screen.getByLabelText('Name')
    expect(input).toHaveAttribute('aria-invalid', 'true')
    const ids = input.getAttribute('aria-describedby')!.split(' ')
    expect(ids).toHaveLength(2)
    expect(document.getElementById(ids[0])).toHaveTextContent('Hint text')
    expect(screen.getByRole('alert')).toHaveTextContent('Bad')
    expect(document.getElementById(ids[1])).toBe(screen.getByRole('alert'))
  })
  it('no invalid without error', () => {
    render(<Field label="Name"><input /></Field>)
    expect(screen.getByLabelText('Name')).not.toHaveAttribute('aria-invalid')
  })
  it('hidden label is still accessible', () => {
    render(<Field label="Note" labelHidden><textarea /></Field>)
    expect(screen.getByLabelText('Note')).toBeInTheDocument()
    expect(screen.getByText('Note')).toHaveClass('sr-only')
  })
  it('respects custom id and child describedby', () => {
    render(<Field label="L" id="mine" hint="h"><input aria-describedby="other" /></Field>)
    const input = screen.getByLabelText('L')
    expect(input).toHaveAttribute('id', 'mine')
    expect(input.getAttribute('aria-describedby')).toBe('other mine-hint')
  })
})
