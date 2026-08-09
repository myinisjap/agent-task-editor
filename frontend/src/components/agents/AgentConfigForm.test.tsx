// AgentConfigForm tests: the "Max turns" field warns when a turn cap is set
// on a provider that doesn't enforce it (codex_cli, opencode), mirroring the
// existing cost-tracking/subtasks/session-resume capability warnings, and
// stays silent for providers where maxTurns support is 'full' (claude).
import { describe, it, expect, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
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
  it('warns when max_turns is set on codex_cli, which does not enforce it', () => {
    const form = baseForm({ provider_config_id: 'pc-codex', max_turns: 20 })
    renderForm(form, [providerConfig('pc-codex', 'codex_cli')])

    expect(
      screen.getByText(/codex exec has no turn-cap flag/i),
    ).toBeInTheDocument()
  })

  it('warns when max_turns is set on opencode, which does not enforce it', () => {
    const form = baseForm({ provider_config_id: 'pc-oc', max_turns: 5 })
    renderForm(form, [providerConfig('pc-oc', 'opencode')])

    expect(
      screen.getByText(/opencode CLI has no turn-cap flag/i),
    ).toBeInTheDocument()
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
