import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import IntakeEndpointsSection from './IntakeEndpointsSection'
import type { IntakeEndpoint, Repo, Workflow } from '../../api/client'

const listMock = vi.fn()
const createMock = vi.fn()
const updateMock = vi.fn()
const testMock = vi.fn()
const reposListMock = vi.fn()
const workflowsListMock = vi.fn()

vi.mock('../../api/client', async () => {
  const actual = await vi.importActual<typeof import('../../api/client')>('../../api/client')
  return {
    ...actual,
    api: {
      ...actual.api,
      intakeEndpoints: {
        list: (...a: unknown[]) => listMock(...a),
        get: vi.fn(),
        create: (...a: unknown[]) => createMock(...a),
        update: (...a: unknown[]) => updateMock(...a),
        delete: vi.fn(),
        rotateSecret: vi.fn(),
        test: (...a: unknown[]) => testMock(...a),
      },
      repos: { ...actual.api.repos, list: (...a: unknown[]) => reposListMock(...a) },
      workflows: { ...actual.api.workflows, list: (...a: unknown[]) => workflowsListMock(...a) },
    },
  }
})

function endpoint(o: Partial<IntakeEndpoint> = {}): IntakeEndpoint {
  return {
    id: 'ep-1',
    name: 'CI alerts',
    repo_id: 'repo-1',
    workflow_id: null,
    target_label: '',
    enabled: true,
    title_template: '{{alert.title}}',
    description_template: '',
    dedupe_key_template: '',
    last_received_at: null,
    created_at: new Date().toISOString(),
    updated_at: new Date().toISOString(),
    secret_set: true,
    url_path: '/api/v1/intake/ep-1',
    ...o,
  }
}

const repo = { id: 'repo-1', name: 'acme/widgets', path: '/tmp/w', workflow_id: 'wf-1', created_at: '' } as Repo
const wf = {
  id: 'wf-1',
  name: 'Default',
  description: '',
  labels: [
    { id: 'l1', workflow_id: 'wf-1', name: 'not_ready', agent_ignore: 1, sort_order: 0 },
    { id: 'l2', workflow_id: 'wf-1', name: 'work', agent_ignore: 0, sort_order: 1 },
  ],
  transitions: [],
} as unknown as Workflow

describe('IntakeEndpointsSection', () => {
  beforeEach(() => {
    listMock.mockReset().mockResolvedValue([endpoint()])
    createMock.mockReset()
    updateMock.mockReset()
    testMock.mockReset()
    reposListMock.mockReset().mockResolvedValue([repo])
    workflowsListMock.mockReset().mockResolvedValue([wf])
  })

  it('lists endpoints', async () => {
    render(<IntakeEndpointsSection />)
    expect(await screen.findByText('CI alerts')).toBeInTheDocument()
    expect(screen.getByText(/gate \(human review\)/)).toBeInTheDocument()
    expect(screen.getByText(/never received/)).toBeInTheDocument()
  })

  it('shows the secret once after create', async () => {
    createMock.mockResolvedValue({ ...endpoint({ id: 'ep-2', name: 'New' }), secret: 'whsec_abc' })
    render(<IntakeEndpointsSection />)
    await screen.findByText('CI alerts')
    await userEvent.click(screen.getByText('+ Add Endpoint'))
    await userEvent.type(screen.getByPlaceholderText('CI alerts'), 'New')
    await userEvent.click(screen.getByText('Create endpoint'))
    expect(await screen.findByText('whsec_abc')).toBeInTheDocument()
    expect(screen.getByText(/won.t be shown again/)).toBeInTheDocument()
    expect(createMock).toHaveBeenCalledWith(expect.objectContaining({ name: 'New', repo_id: 'repo-1' }))
  })

  it('toggles enabled via update', async () => {
    updateMock.mockResolvedValue(endpoint({ enabled: false }))
    render(<IntakeEndpointsSection />)
    await userEvent.click(await screen.findByLabelText('Enable CI alerts'))
    await waitFor(() => expect(updateMock).toHaveBeenCalledWith('ep-1', expect.objectContaining({ enabled: false })))
  })

  it('previews a sample payload', async () => {
    testMock.mockResolvedValue({
      title: 'Rendered title',
      description: 'desc',
      dedupe_key: 'k1',
      label: 'not_ready',
      workflow_id: 'wf-1',
      priority: 0,
      matched_rule_id: null,
      existing_task_id: null,
    })
    render(<IntakeEndpointsSection />)
    await screen.findByText('CI alerts')
    await userEvent.click(screen.getByText('Edit'))
    await userEvent.click(screen.getByText('Test payload'))
    expect(await screen.findByText('Rendered title')).toBeInTheDocument()
    expect(testMock).toHaveBeenCalledWith('ep-1', expect.objectContaining({ title_template: '{{alert.title}}' }))
  })
})
