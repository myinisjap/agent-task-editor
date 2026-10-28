import { describe, it, expect, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import LessonsSettingsPage from './LessonsSettingsPage'

const listGlobal = vi.fn().mockResolvedValue([])
vi.mock('../api/client', () => ({
  api: { lessons: { listGlobal: (...a: unknown[]) => listGlobal(...a) }, repos: {} },
}))

describe('LessonsSettingsPage', () => {
  it('renders the global lessons panel', async () => {
    render(<MemoryRouter><LessonsSettingsPage /></MemoryRouter>)
    expect(screen.getByRole('heading', { name: 'Global lessons', level: 1 })).toBeInTheDocument()
    expect(await screen.findByText('No lessons yet.')).toBeInTheDocument()
    expect(listGlobal).toHaveBeenCalledWith(undefined)
  })
})
