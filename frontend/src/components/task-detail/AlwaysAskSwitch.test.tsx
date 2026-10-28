import { render, screen, fireEvent } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import AlwaysAskSwitch from './AlwaysAskSwitch'

describe('AlwaysAskSwitch', () => {
  it('reflects state and toggles', () => {
    const onChange = vi.fn()
    render(<AlwaysAskSwitch checked={false} onChange={onChange} />)
    const sw = screen.getByRole('switch', { name: 'Always ask for this task' })
    expect(sw).toHaveAttribute('aria-checked', 'false')
    fireEvent.click(sw)
    expect(onChange).toHaveBeenCalledWith(true)
  })
})
