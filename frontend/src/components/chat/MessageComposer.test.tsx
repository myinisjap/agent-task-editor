import { render, screen, fireEvent } from '@testing-library/react'
import { test, expect, vi } from 'vitest'
import MessageComposer from './MessageComposer'

function setup(directMode = false) {
  const onSend = vi.fn()
  const onToggleDirect = vi.fn()
  render(<MessageComposer onSend={onSend} directMode={directMode} onToggleDirect={onToggleDirect} />)
  return { onSend, onToggleDirect }
}

test('sends text with carriage return and clears', () => {
  const { onSend } = setup()
  const ta = screen.getByLabelText('Message') as HTMLTextAreaElement
  fireEvent.change(ta, { target: { value: 'hi' } })
  fireEvent.click(screen.getByRole('button', { name: 'Send' }))
  expect(onSend).toHaveBeenCalledWith('hi\r')
  expect(ta.value).toBe('')
})
test('multi-line uses bracketed paste', () => {
  const { onSend } = setup()
  fireEvent.change(screen.getByLabelText('Message'), { target: { value: 'a\nb' } })
  fireEvent.click(screen.getByRole('button', { name: 'Send' }))
  expect(onSend).toHaveBeenCalledWith('\x1b[200~a\nb\x1b[201~\r')
})
test('empty send is just Enter', () => {
  const { onSend } = setup()
  fireEvent.click(screen.getByRole('button', { name: 'Send' }))
  expect(onSend).toHaveBeenCalledWith('\r')
})
test('keyboard toggle', () => {
  const { onToggleDirect } = setup()
  fireEvent.click(screen.getByRole('button', { name: /type directly/i }))
  expect(onToggleDirect).toHaveBeenCalled()
})
