import { render } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { Pause } from 'lucide-react'
import Icon from './Icon'

describe('Icon', () => {
  it('renders an aria-hidden svg with the size class', () => {
    const { container } = render(<Icon icon={Pause} size="sm" className="text-danger" />)
    const svg = container.querySelector('svg')!
    expect(svg.getAttribute('aria-hidden')).toBe('true')
    expect(svg.getAttribute('class')).toContain('h-3.5')
    expect(svg.getAttribute('class')).toContain('text-danger')
  })
})
