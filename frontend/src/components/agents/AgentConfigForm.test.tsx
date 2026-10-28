// AgentConfigForm tests: the "Max turns" field would warn when a turn cap is
// set on a provider that doesn't enforce it, mirroring the existing
// cost-tracking/subtasks/session-resume capability warnings. Every provider
// (claude, qwen_code, codex_cli, opencode) now has maxTurns support 'full',
// so these tests assert the warning stays silent across the board.
import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import AgentConfigForm, { type FormState } from './AgentConfigForm'
import type { ProviderConfig } from '../../api/client'

function baseForm(overrides: Partial<FormState> = {}): FormState {
  return {
    name: 'Test agent',
    provider_config_id: 'pc-1',
    system_prompt: '',
    labels: '[]',
    timeout_secs: 600,
    max_tokens: 8000,
    max_turns: 0,
    max_retries: 3,
    retry_backoff_secs: 30,
    priority: 0,
    max_cost_usd: 0,
    effort: '',
    permission_mode: '',
    resume_sessions: true,
    subtasks_enabled: false,
    max_subtasks: 10,
    enabled_plugins: '[]',
    enabled_mcp_servers: '[]',
    command_allowlist: '[]',
    command_denylist: '[]',
    ...overrides,
  } as FormState
}

function providerConfig(id: string, provider: string): ProviderConfig {
  return {
    id,
    name: `${provider} config`,
    provider,
    model: null,
    created_at: '2026-01-01T00:00:00Z',
    updated_at: '2026-01-01T00:00:00Z',
  } as unknown as ProviderConfig
}

function renderForm(form: FormState, providerConfigs: ProviderConfig[]) {
  return render(
    <MemoryRouter>
      <AgentConfigForm
        selected={null}
        form={form}
        setForm={vi.fn()}
        availableLabels={[]}
        providerConfigs={providerConfigs}
        claudeOptions={null}
        saving={false}
        deleting={false}
        onSave={vi.fn()}
        onDelete={vi.fn()}
        onToggleEnabled={vi.fn()}
      />
    </MemoryRouter>,
  )
}

describe('AgentConfigForm max_turns capability warning', () => {
  it('does not warn when max_turns is set on codex_cli, whose runner enforces it', () => {
    const form = baseForm({ provider_config_id: 'pc-codex', max_turns: 20 })
    renderForm(form, [providerConfig('pc-codex', 'codex_cli')])

    expect(screen.queryByText(/turn-cap flag/i)).not.toBeInTheDocument()
  })

  it('does not warn when max_turns is set on opencode, whose runner and config.steps now enforce it', () => {
    const form = baseForm({ provider_config_id: 'pc-oc', max_turns: 5 })
    renderForm(form, [providerConfig('pc-oc', 'opencode')])

    expect(screen.queryByText(/turn-cap flag/i)).not.toBeInTheDocument()
  })

  it('does not warn when max_turns is set on claude, which enforces it', () => {
    const form = baseForm({ provider_config_id: 'pc-claude', max_turns: 20 })
    renderForm(form, [providerConfig('pc-claude', 'claude')])

    expect(screen.queryByText(/turn-cap flag/i)).not.toBeInTheDocument()
  })

  it('does not warn on codex_cli when max_turns is 0 (unset)', () => {
    const form = baseForm({ provider_config_id: 'pc-codex', max_turns: 0 })
    renderForm(form, [providerConfig('pc-codex', 'codex_cli')])

    expect(screen.queryByText(/turn-cap flag/i)).not.toBeInTheDocument()
  })
})

describe('AgentConfigForm effort capability warning', () => {
  it('does not warn on claude, which has full effort support', () => {
    const form = baseForm({ provider_config_id: 'pc-claude', effort: 'high' })
    renderForm(form, [providerConfig('pc-claude', 'claude')])

    expect(screen.getByText('Effort')).toBeInTheDocument()
    expect(screen.queryByText(/will be ignored/i)).not.toBeInTheDocument()
  })

  it('warns when effort is set on qwen_code, which does not support it', () => {
    const form = baseForm({ provider_config_id: 'pc-qwen', effort: 'medium' })
    renderForm(form, [providerConfig('pc-qwen', 'qwen_code')])

    expect(screen.getByText(/no reasoning-effort flag on the qwen CLI/i)).toBeInTheDocument()
  })

  it('does not warn when effort is unset ("")', () => {
    const form = baseForm({ provider_config_id: 'pc-qwen', effort: '' })
    renderForm(form, [providerConfig('pc-qwen', 'qwen_code')])

    expect(screen.queryByText(/no reasoning-effort flag/i)).not.toBeInTheDocument()
  })
})

describe('AgentConfigForm permission mode field', () => {
  it('renders the field for claude', () => {
    const form = baseForm({ provider_config_id: 'pc-claude' })
    renderForm(form, [providerConfig('pc-claude', 'claude')])

    expect(screen.getByText('Permission mode')).toBeInTheDocument()
  })

  it('is hidden for non-claude providers', () => {
    const form = baseForm({ provider_config_id: 'pc-qwen' })
    renderForm(form, [providerConfig('pc-qwen', 'qwen_code')])

    expect(screen.queryByText('Permission mode')).not.toBeInTheDocument()
  })

  it('warns that auto depends on a cloud classifier', () => {
    const form = baseForm({ provider_config_id: 'pc-claude', permission_mode: 'auto' })
    renderForm(form, [providerConfig('pc-claude', 'claude')])

    expect(screen.getByText(/transiently deny harmless commands/i)).toBeInTheDocument()
  })

  it('warns that bypassPermissions skips approval prompts', () => {
    const form = baseForm({ provider_config_id: 'pc-claude', permission_mode: 'bypassPermissions' })
    renderForm(form, [providerConfig('pc-claude', 'claude')])

    expect(screen.getByText(/this mode skips approval prompts entirely/i)).toBeInTheDocument()
  })

  it('always shows the guardrail-not-sandbox note on the command filters', () => {
    renderForm(baseForm(), [providerConfig('pc-claude', 'claude')])

    expect(screen.getByText(/these lists are guardrails, not a sandbox/i)).toBeInTheDocument()
  })

  it('notes the denylist is still enforced under bypassPermissions when a denylist is set', () => {
    const form = baseForm({
      provider_config_id: 'pc-claude',
      permission_mode: 'bypassPermissions',
      command_denylist: '["rm -rf*"]',
    })
    renderForm(form, [providerConfig('pc-claude', 'claude')])

    expect(screen.getByText(/command_denylist is still enforced/i)).toBeInTheDocument()
  })

  it('does not mention the denylist under bypassPermissions when no denylist is set', () => {
    const form = baseForm({
      provider_config_id: 'pc-claude',
      permission_mode: 'bypassPermissions',
      command_denylist: '[]',
    })
    renderForm(form, [providerConfig('pc-claude', 'claude')])

    expect(screen.queryByText(/command_denylist is still enforced/i)).not.toBeInTheDocument()
  })

  it('does not warn when unset ("")', () => {
    const form = baseForm({ provider_config_id: 'pc-claude', permission_mode: '' })
    renderForm(form, [providerConfig('pc-claude', 'claude')])

    expect(screen.queryByText(/transiently deny harmless commands/i)).not.toBeInTheDocument()
    expect(screen.queryByText(/this mode skips approval prompts entirely/i)).not.toBeInTheDocument()
  })
})

describe('AgentConfigForm labels and field order', () => {
  it('labels the cost field as a cumulative per-task budget', () => {
    renderForm(baseForm(), [providerConfig('pc-1', 'claude')])
    expect(screen.getByText('Max cost per task (USD)')).toBeInTheDocument()
    expect(screen.queryByText(/max cost per run/i)).not.toBeInTheDocument()
    expect(screen.getByText(/cumulative across all runs of a task/i)).toBeInTheDocument()
  })

  it('renders labels and system prompt before the tuning fields', () => {
    renderForm(baseForm(), [providerConfig('pc-1', 'claude')])
    const pos = (text: string) => {
      const el = screen.getByText(text, { selector: 'label' })
      return el
    }
    const before = (a: HTMLElement, b: HTMLElement) =>
      Boolean(a.compareDocumentPosition(b) & Node.DOCUMENT_POSITION_FOLLOWING)
    const labels = pos('Labels')
    const prompt = pos('System prompt')
    expect(before(pos('Provider config'), labels)).toBe(true)
    expect(before(labels, prompt)).toBe(true)
    for (const knob of ['Timeout (secs)', 'Max tokens', 'Max turns', 'Max retries', 'Max cost per task (USD)']) {
      expect(before(prompt, pos(knob))).toBe(true)
    }
  })
})

describe('AgentConfigForm fallback providers', () => {
  it('lists fallbacks in order and offers the remaining provider configs', () => {
    const form = baseForm({ provider_config_id: 'pc-claude', fallback_provider_config_ids: ['pc-codex'] })
    renderForm(form, [
      providerConfig('pc-claude', 'claude'),
      providerConfig('pc-codex', 'codex_cli'),
      providerConfig('pc-qwen', 'qwen_code'),
    ])

    const list = screen.getByRole('list', { name: 'Fallback providers' })
    expect(list).toHaveTextContent('codex_cli config')
    const add = screen.getByRole('combobox', { name: 'Add fallback provider' })
    expect(add).toHaveTextContent('qwen_code config')
    expect(add).not.toHaveTextContent('claude config')
    expect(add).not.toHaveTextContent('codex_cli config')
  })

  it('warns when a fallback provider cannot honour a setting the agent uses', () => {
    const form = baseForm({ provider_config_id: 'pc-claude', effort: 'high', fallback_provider_config_ids: ['pc-qwen'] })
    renderForm(form, [providerConfig('pc-claude', 'claude'), providerConfig('pc-qwen', 'qwen_code')])

    const alert = screen.getByRole('alert')
    expect(alert).toHaveTextContent(/falls back to/i)
    expect(alert).toHaveTextContent('qwen_code')
    expect(alert).toHaveTextContent('Effort')
  })

  it('shows no fallback warning when the fallbacks support every setting in use', () => {
    const form = baseForm({ provider_config_id: 'pc-qwen', effort: 'high', fallback_provider_config_ids: ['pc-claude'] })
    renderForm(form, [providerConfig('pc-claude', 'claude'), providerConfig('pc-qwen', 'qwen_code')])

    expect(screen.queryByText(/falls back to/i)).not.toBeInTheDocument()
  })

  it('reorders and removes fallbacks through setForm', () => {
    const setForm = vi.fn()
    const form = baseForm({ provider_config_id: 'pc-claude', fallback_provider_config_ids: ['pc-codex', 'pc-qwen'] })
    render(
      <MemoryRouter>
        <AgentConfigForm
          selected={null}
          form={form}
          setForm={setForm}
          availableLabels={[]}
          providerConfigs={[providerConfig('pc-claude', 'claude'), providerConfig('pc-codex', 'codex_cli'), providerConfig('pc-qwen', 'qwen_code')]}
          claudeOptions={null}
          saving={false}
          deleting={false}
          onSave={vi.fn()}
          onDelete={vi.fn()}
          onToggleEnabled={vi.fn()}
        />
      </MemoryRouter>,
    )
    const apply = () => setForm.mock.calls.at(-1)![0](form)

    fireEvent.click(screen.getByRole('button', { name: 'Move qwen_code config up' }))
    expect(apply().fallback_provider_config_ids).toEqual(['pc-qwen', 'pc-codex'])

    fireEvent.click(screen.getByRole('button', { name: 'Remove codex_cli config' }))
    expect(apply().fallback_provider_config_ids).toEqual(['pc-qwen'])

    fireEvent.change(screen.getByRole('combobox', { name: 'Add fallback provider' }), { target: { value: 'pc-claude' } })
    // pc-claude is the primary, so it isn't offered; nothing is added.
    expect(setForm.mock.calls.length).toBe(2)
  })
})

describe('AgentConfigForm escalation ladder', () => {
  it('adds a rung via setForm updater', () => {
    const setForm = vi.fn()
    render(
      <MemoryRouter>
        <AgentConfigForm
          selected={null}
          form={baseForm()}
          setForm={setForm}
          availableLabels={[]}
          providerConfigs={[providerConfig('pc-1', 'claude')]}
          claudeOptions={null}
          saving={false}
          deleting={false}
          onSave={vi.fn()}
          onDelete={vi.fn()}
          onToggleEnabled={vi.fn()}
        />
      </MemoryRouter>,
    )
    fireEvent.click(screen.getByText('Add rung'))
    const updater = setForm.mock.calls[0][0] as (f: FormState) => FormState
    expect(updater(baseForm()).escalation_ladder).toEqual([{ provider_config_id: 'pc-1', effort: '' }])
  })

  it('renders existing rungs with reorder and remove controls', () => {
    renderForm(
      baseForm({ escalation_ladder: [{ provider_config_id: 'pc-1', effort: 'max' }, { provider_config_id: 'pc-1' }] }),
      [providerConfig('pc-1', 'claude')],
    )
    expect(screen.getByLabelText('Rung 1 effort')).toHaveValue('max')
    expect(screen.getByLabelText('Move rung 2 up')).toBeEnabled()
    expect(screen.getByLabelText('Remove rung 2')).toBeInTheDocument()
  })
})

describe('AgentConfigForm — Skills', () => {
  const skill = { id: 's1', name: 'alpha', description: '', enabled: true, created_at: '', updated_at: '' }
  const renderForm = (provider: string, onSkillIdsChange = vi.fn()) =>
    render(
      <MemoryRouter>
        <AgentConfigForm
          selected={null}
          form={baseForm()}
          setForm={vi.fn()}
          availableLabels={[]}
          providerConfigs={[providerConfig('pc-1', provider)]}
          claudeOptions={null}
          saving={false}
          deleting={false}
          onSave={vi.fn()}
          onDelete={vi.fn()}
          onToggleEnabled={vi.fn()}
          skills={[skill]}
          skillIds={[]}
          onSkillIdsChange={onSkillIdsChange}
        />
      </MemoryRouter>,
    )

  it('renders the picker and reports changes', () => {
    const onChange = vi.fn()
    renderForm('claude', onChange)
    fireEvent.click(screen.getByRole('button', { name: 'alpha' }))
    expect(onChange).toHaveBeenCalledWith(['s1'])
    expect(screen.queryByText(/no skills support/)).toBeNull()
  })

  it('warns when the provider lacks skills support', () => {
    renderForm('anthropic')
    expect(screen.getByText(/anthropic provider has no skills support/)).toBeInTheDocument()
  })
})
