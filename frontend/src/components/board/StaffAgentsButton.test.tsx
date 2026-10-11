// StaffAgentsButton tests: one-click creation of the missing template agents
// against a chosen provider config; no-provider hint; inline API errors.
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import StaffAgentsButton from './StaffAgentsButton'
import type { ProviderConfig } from '../../api/client'
import { useProviderConfigsStore } from '../../stores/providerConfigs'
import { useAgentsStore } from '../../stores/agents'

const createMock = vi.fn()
const listMock = vi.fn()

vi.mock('../../api/client', async () => {
  const actual = await vi.importActual<typeof import('../../api/client')>('../../api/client')
  return {
    ...actual,
    api: {
      agents: {
        create: (...a: unknown[]) => createMock(...a),
        list: (...a: unknown[]) => listMock(...a),
      },
    },
  }
})

const pc = (id: string, name: string) => ({ id, name, provider: 'claude', model: 'm', env: '{}' }) as unknown as ProviderConfig
const ALL = ['plan', 'work', 'testing', 'agent-review']

function renderButton(labels = ALL) {
  return render(
    <MemoryRouter>
      <StaffAgentsButton missingLabels={labels} />
    </MemoryRouter>,
  )
}

beforeEach(() => {
  vi.clearAllMocks()
  useProviderConfigsStore.setState({ configs: [pc('pc1', 'main')] })
  useAgentsStore.setState({ configs: [] })
  createMock.mockImplementation(async (body: { name: string }) => ({ config: { id: body.name, ...body } }))
  listMock.mockResolvedValue([])
})

describe('StaffAgentsButton', () => {
  it('creates all four agents against the only provider config', async () => {
    const user = userEvent.setup()
    renderButton()

    await user.click(screen.getByRole('button', { name: 'Create Planner, Worker, Tester, Reviewer' }))

    await waitFor(() => expect(createMock).toHaveBeenCalledTimes(4))
    const bodies = createMock.mock.calls.map((c) => c[0])
    expect(bodies.map((b) => b.name)).toEqual(['Planner', 'Worker', 'Tester', 'Reviewer'])
    expect(bodies.map((b) => b.labels)).toEqual(['["plan"]', '["work"]', '["testing"]', '["agent-review"]'])
    expect(bodies.every((b) => b.provider_config_id === 'pc1')).toBe(true)
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })

  it('only creates agents for the missing labels', async () => {
    const user = userEvent.setup()
    renderButton(['testing'])

    await user.click(screen.getByRole('button', { name: 'Create Tester' }))

    await waitFor(() => expect(createMock).toHaveBeenCalledTimes(1))
    expect(createMock.mock.calls[0][0].name).toBe('Tester')
  })

  it('uses the provider config picked in the selector', async () => {
    const user = userEvent.setup()
    useProviderConfigsStore.setState({ configs: [pc('pc1', 'main'), pc('pc2', 'other')] })
    renderButton(['plan'])

    await user.selectOptions(screen.getByLabelText('Provider config'), 'pc2')
    await user.click(screen.getByRole('button', { name: 'Create Planner' }))

    await waitFor(() => expect(createMock).toHaveBeenCalled())
    expect(createMock.mock.calls[0][0].provider_config_id).toBe('pc2')
  })

  it('is disabled with a hint when no provider config exists', () => {
    useProviderConfigsStore.setState({ configs: [] })
    renderButton()

    expect(screen.getByRole('button', { name: /Create Planner/ })).toBeDisabled()
    expect(screen.getByText(/Configure a provider first/)).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Providers →' })).toHaveAttribute('href', '/providers')
  })

  it('shows an inline error and stops when the API fails', async () => {
    const user = userEvent.setup()
    createMock.mockImplementation(async (body: { name: string }) => {
      if (body.name === 'Worker') throw new Error('name already exists')
      return { config: { id: body.name } }
    })
    renderButton()

    await user.click(screen.getByRole('button', { name: /Create Planner/ }))

    expect(await screen.findByRole('alert')).toHaveTextContent('Could not create Worker: name already exists')
    expect(createMock).toHaveBeenCalledTimes(2)
    // Button is usable again for a retry.
    expect(screen.getByRole('button', { name: /Create Planner/ })).toBeEnabled()
  })

  it('points at the agents page for labels with no built-in template', () => {
    renderButton(['custom'])
    expect(screen.getByText(/No built-in template for: custom/)).toBeInTheDocument()
    expect(screen.queryByRole('button')).not.toBeInTheDocument()
  })
})
