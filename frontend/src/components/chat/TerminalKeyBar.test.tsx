import { render, screen, fireEvent } from '@testing-library/react'
import { test, expect, vi } from 'vitest'
import TerminalKeyBar from './TerminalKeyBar'

function setup(ctrlArmed = false) {
  const onKey = vi.fn()
  const onToggleCtrl = vi.fn()
  render(<TerminalKeyBar onKey={onKey} onToggleCtrl={onToggleCtrl} ctrlArmed={ctrlArmed} />)
  return { onKey, onToggleCtrl }
}

test('pressing a key writes its raw sequence', () => {
  const { onKey } = setup()
  fireEvent.click(screen.getByRole('button', { name: 'Tab' }))
  expect(onKey).toHaveBeenCalledWith('\t')
  fireEvent.click(screen.getByRole('button', { name: 'Up arrow' }))
  expect(onKey).toHaveBeenCalledWith('\x1b[A')
})

test('Ctrl is a toggle, not a keystroke', () => {
  const { onKey, onToggleCtrl } = setup()
  fireEvent.click(screen.getByRole('button', { name: 'Ctrl' }))
  expect(onToggleCtrl).toHaveBeenCalledOnce()
  expect(onKey).not.toHaveBeenCalled()
})

test('armed Ctrl is exposed to assistive tech', () => {
  setup(true)
  expect(screen.getByRole('button', { name: 'Ctrl' })).toHaveAttribute('aria-pressed', 'true')
})

test('buttons suppress the pointerdown default so focus stays on the terminal', () => {
  // Losing focus on xterm's hidden textarea dismisses the on-screen keyboard,
  // which would make the bar unusable for anything but one-off keys.
  setup()
  const tab = screen.getByRole('button', { name: 'Tab' })
  const ev = new PointerEvent('pointerdown', { bubbles: true, cancelable: true })
  fireEvent(tab, ev)
  expect(ev.defaultPrevented).toBe(true)
})
