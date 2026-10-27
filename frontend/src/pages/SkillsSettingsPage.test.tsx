import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import SkillsSettingsPage from './SkillsSettingsPage'
import type { Skill } from '../api/client'

const m = vi.hoisted(() => ({
  list: vi.fn(), get: vi.fn(), createFromMarkdown: vi.fn(), createFromZip: vi.fn(), update: vi.fn(), del: vi.fn(),
  agents: vi.fn(), repos: vi.fn(),
}))

vi.mock('../api/client', async () => {
  const actual = await vi.importActual<typeof import('../api/client')>('../api/client')
  return {
    ...actual,
    api: {
      ...actual.api,
      skills: { list: m.list, get: m.get, createFromMarkdown: m.createFromMarkdown, createFromZip: m.createFromZip, update: m.update, delete: m.del },
      agents: { ...actual.api.agents, list: m.agents },
      repos: { ...actual.api.repos, list: m.repos },
    },
  }
})

const sk = (o: Partial<Skill> = {}): Skill => ({ id: 's1', name: 'alpha', description: 'Alpha desc', enabled: true, created_at: '', updated_at: '', ...o })

beforeEach(() => {
  Object.values(m).forEach((f) => f.mockReset())
  m.list.mockResolvedValue([sk({ agent_config_ids: ['a1'], repo_ids: ['r1', 'rX'] })])
  m.agents.mockResolvedValue([{ id: 'a1', name: 'Coder' }])
  m.repos.mockResolvedValue([{ id: 'r1', name: 'widgets' }])
})

describe('SkillsSettingsPage', () => {
  it('lists skills with attachments', async () => {
    render(<SkillsSettingsPage />)
    expect(await screen.findByText('alpha')).toBeInTheDocument()
    expect(screen.getByText('Alpha desc')).toBeInTheDocument()
    expect(screen.getByLabelText('Enable alpha')).toBeChecked()
    expect(await screen.findByText('Agent: Coder')).toBeInTheDocument()
    expect(screen.getByText('Repo: widgets')).toBeInTheDocument()
    expect(screen.getByText('Repo: Unknown')).toBeInTheDocument()
  })

  it('pastes SKILL.md and shows server errors', async () => {
    render(<SkillsSettingsPage />)
    await screen.findByText('alpha')
    m.createFromMarkdown.mockRejectedValueOnce(new Error('a skill with that name already exists'))
    await userEvent.type(screen.getByLabelText('SKILL.md contents'), 'hello')
    await userEvent.click(screen.getByRole('button', { name: 'Add skill' }))
    expect(await screen.findByText(/already exists/)).toBeInTheDocument()
    m.createFromMarkdown.mockResolvedValueOnce(sk({ id: 's2', name: 'beta', description: 'B' }))
    await userEvent.click(screen.getByRole('button', { name: 'Add skill' }))
    expect(await screen.findByText('beta')).toBeInTheDocument()
    expect(m.createFromMarkdown).toHaveBeenCalledWith('hello')
  })

  it('uploads a zip and rejects oversize files', async () => {
    render(<SkillsSettingsPage />)
    await screen.findByText('alpha')
    await userEvent.click(screen.getByRole('tab', { name: 'Upload zip' }))
    const big = new File([new Uint8Array(1024 * 1024 + 1)], 'big.zip', { type: 'application/zip' })
    await userEvent.upload(screen.getByLabelText('Skill zip file'), big)
    expect(screen.getByText(/larger than 1 MB/)).toBeInTheDocument()
    expect(m.createFromZip).not.toHaveBeenCalled()

    const f = new File(['x'], 's.zip', { type: 'application/zip' })
    m.createFromZip.mockResolvedValueOnce(sk({ id: 's3', name: 'zed' }))
    await userEvent.upload(screen.getByLabelText('Skill zip file'), f)
    await userEvent.click(screen.getByRole('button', { name: 'Add skill' }))
    await waitFor(() => expect(m.createFromZip).toHaveBeenCalledWith(f))
    expect(await screen.findByText('zed')).toBeInTheDocument()
  })

  it('views contents', async () => {
    m.get.mockResolvedValue(sk({ skill_md: '# body text', files: ['ref/a.md'] }))
    render(<SkillsSettingsPage />)
    await screen.findByText('alpha')
    await userEvent.click(screen.getByRole('button', { name: 'View' }))
    expect(await screen.findByText('# body text')).toBeInTheDocument()
    expect(screen.getByText('ref/a.md')).toBeInTheDocument()
  })

  it('toggles enabled', async () => {
    m.update.mockResolvedValue(sk({ enabled: false }))
    render(<SkillsSettingsPage />)
    await userEvent.click(await screen.findByLabelText('Enable alpha'))
    expect(m.update).toHaveBeenCalledWith('s1', { enabled: false })
    await waitFor(() => expect(screen.getByLabelText('Enable alpha')).not.toBeChecked())
  })

  it('deletes with an in-page confirm', async () => {
    const confirmSpy = vi.spyOn(window, 'confirm')
    m.del.mockResolvedValue(undefined)
    render(<SkillsSettingsPage />)
    await screen.findByText('alpha')
    await userEvent.click(screen.getByRole('button', { name: 'Delete' }))
    expect(screen.getByText(/detaches it from 1 agents \/ 2 repos/)).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: 'Cancel' }))
    expect(screen.getByText('alpha')).toBeInTheDocument()
    expect(m.del).not.toHaveBeenCalled()
    await userEvent.click(screen.getByRole('button', { name: 'Delete' }))
    await userEvent.click(screen.getByRole('button', { name: 'Confirm delete' }))
    await waitFor(() => expect(screen.queryByText('alpha')).not.toBeInTheDocument())
    expect(m.del).toHaveBeenCalledWith('s1')
    expect(confirmSpy).not.toHaveBeenCalled()
  })
})
