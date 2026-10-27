import { describe, it, expect, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import SkillPicker from './SkillPicker'
import type { Skill } from '../../api/client'

const sk = (id: string, name: string, enabled = true): Skill => ({ id, name, description: '', enabled, created_at: '', updated_at: '' })

describe('SkillPicker', () => {
  it('toggles chips', async () => {
    const onChange = vi.fn()
    render(<MemoryRouter><SkillPicker skills={[sk('1', 'a'), sk('2', 'b')]} selected={['1']} onChange={onChange} /></MemoryRouter>)
    await userEvent.click(screen.getByRole('button', { name: 'b' }))
    expect(onChange).toHaveBeenCalledWith(['1', '2'])
    await userEvent.click(screen.getByRole('button', { name: 'a' }))
    expect(onChange).toHaveBeenCalledWith([])
  })
  it('shows empty message', () => {
    render(<MemoryRouter><SkillPicker skills={[]} selected={[]} onChange={() => {}} /></MemoryRouter>)
    expect(screen.getByText(/No skills yet/)).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Manage skills' })).toHaveAttribute('href', '/settings/skills')
  })
  it('labels disabled skills', () => {
    render(<MemoryRouter><SkillPicker skills={[sk('1', 'a', false)]} selected={[]} onChange={() => {}} /></MemoryRouter>)
    expect(screen.getByRole('button', { name: 'a (disabled)' })).toBeInTheDocument()
    expect(screen.getByText(/not copied/)).toBeInTheDocument()
  })
})
