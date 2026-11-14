import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import RecipesSection from './RecipesSection'
import type { AutomationRecipe, Repo, Workflow } from '../../api/client'

const listMock = vi.fn()
const installMock = vi.fn()
const removeMock = vi.fn()

vi.mock('../../api/client', async () => {
  const actual = await vi.importActual<typeof import('../../api/client')>('../../api/client')
  return {
    ...actual,
    api: {
      ...actual.api,
      automationRecipes: {
        list: (...a: unknown[]) => listMock(...a),
        install: (...a: unknown[]) => installMock(...a),
        remove: (...a: unknown[]) => removeMock(...a),
      },
      repos: { ...actual.api.repos, list: () => Promise.resolve([repo]) },
      workflows: { ...actual.api.workflows, list: () => Promise.resolve([wf]) },
    },
  }
})

const repo = { id: 'repo-1', name: 'acme/widgets', path: '/tmp/w', workflow_id: 'wf-1', created_at: '' } as Repo
const wf = {
  id: 'wf-1',
  name: 'Default',
  description: '',
  labels: [
    { id: 'l1', name: 'not_ready', agent_ignore: true },
    { id: 'l2', name: 'work', agent_ignore: false },
  ],
} as unknown as Workflow

function recipe(o: Partial<AutomationRecipe> = {}): AutomationRecipe {
  return {
    id: 'docs-drift-check',
    version: 1,
    name: 'Docs drift check',
    description: 'Weekly docs check',
    kind: 'schedule',
    params: [
      { key: 'repo_id', label: 'Repository', kind: 'repo', required: true, default: '', help: '' },
      { key: 'target_label', label: 'Target label', kind: 'label', required: false, default: '', help: '' },
    ],
    template: { name: 't', title: 't', description: '', type: 'chore' },
    installed: false,
    objects: { template_id: null, schedule_id: null, endpoint_id: null, rule_id: null, repo_id: null },
    ...o,
  }
}

describe('RecipesSection', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('installs parked by default', async () => {
    listMock.mockResolvedValue([recipe()])
    installMock.mockResolvedValue({ recipe: 'docs-drift-check', template: {}, target_label: 'not_ready', warnings: [] })
    render(<RecipesSection />)
    await userEvent.click(await screen.findByRole('button', { name: 'Install' }))
    await userEvent.click(screen.getByRole('button', { name: 'Install recipe' }))
    await waitFor(() =>
      expect(installMock).toHaveBeenCalledWith('docs-drift-check', {
        params: { repo_id: 'repo-1' },
        confirm_auto_start: false,
      }),
    )
  })

  it('requires confirmation for an agent-triggerable label', async () => {
    listMock.mockResolvedValue([recipe()])
    render(<RecipesSection />)
    await userEvent.click(await screen.findByRole('button', { name: 'Install' }))
    await userEvent.selectOptions(screen.getByLabelText(/Target label/), 'work')
    expect(screen.getByRole('button', { name: 'Install recipe' })).toBeDisabled()
    await userEvent.click(screen.getByRole('checkbox'))
    expect(screen.getByRole('button', { name: 'Install recipe' })).toBeEnabled()
  })

  it('shows installed state and removes', async () => {
    listMock.mockResolvedValue([recipe({ installed: true })])
    removeMock.mockResolvedValue(undefined)
    render(<RecipesSection />)
    expect(await screen.findByText('Installed')).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: 'Remove recipe' }))
    const buttons = await screen.findAllByRole('button', { name: 'Remove recipe' })
    await userEvent.click(buttons[buttons.length - 1])
    await waitFor(() => expect(removeMock).toHaveBeenCalledWith('docs-drift-check'))
  })
})
