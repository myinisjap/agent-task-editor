import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import AutonomyDialChip from './AutonomyDialChip'

describe('AutonomyDialChip', () => {
  it.each([
    ['ask', 'Always ask'],
    ['on_flags', 'Ask on flags'],
    ['auto', 'Auto'],
  ] as const)('shows %s', (mode, text) => {
    render(<AutonomyDialChip mode={mode} />)
    expect(screen.getByText(text)).toBeInTheDocument()
  })
})
