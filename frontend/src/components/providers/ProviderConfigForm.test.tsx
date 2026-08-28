import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useState } from 'react'
import ProviderConfigForm, { type FormState } from './ProviderConfigForm'
import type { ProviderConfig } from '../../api/client'

// Regression test: anthropic/llm/openai were removed from the PROVIDERS
// dropdown when deprecated. Rendering the form for an *existing* config on a
// deprecated provider must not silently coerce form.provider to something
// else (e.g. the first entry in PROVIDERS) just because that value has no
// matching <option> — that would rewrite the config's provider on the next
// save of any unrelated field, which is the whole "existing configs keep
// working" guarantee this deprecation depends on.

function Harness({ initialProvider }: { initialProvider: FormState['provider'] }) {
  const [form, setForm] = useState<FormState>({
    name: 'legacy-anthropic',
    provider: initialProvider,
    model: 'claude-3-opus',
    env: '{}',
  })
  const selected: ProviderConfig = {
    id: 'pc1',
    name: form.name,
    provider: form.provider,
    model: form.model,
    env: form.env,
    created_at: new Date().toISOString(),
    updated_at: new Date().toISOString(),
  }
  return (
    <div>
      <div data-testid="current-provider">{form.provider}</div>
      <ProviderConfigForm
        selected={selected}
        form={form}
        setForm={setForm}
        modelList={null}
        fetchingModels={false}
        saving={false}
        deleting={false}
        onSave={() => {}}
        onDelete={() => {}}
      />
    </div>
  )
}

describe('ProviderConfigForm — deprecated provider round-trip', () => {
  it('keeps an existing anthropic config on "anthropic" after editing an unrelated field', async () => {
    const user = userEvent.setup()
    render(<Harness initialProvider="anthropic" />)

    // The select's displayed value must still be "anthropic", not blanked or
    // snapped to the first PROVIDERS entry. The form renders two <select>s
    // (provider, model); the provider one is first.
    const [select] = screen.getAllByRole('combobox') as HTMLSelectElement[]
    expect(select.value).toBe('anthropic')
    expect(screen.getByTestId('current-provider').textContent).toBe('anthropic')

    // A deprecation warning should be visible (the warning <p>, distinct
    // from the disabled "(deprecated)" dropdown option's label text).
    expect(screen.getByText(/is deprecated, disabled for new configs/i)).toBeInTheDocument()

    // Edit an unrelated field (name) — the provider must not change as a
    // side effect.
    const nameInput = screen.getByPlaceholderText(/e\.g\. claude/i)
    await user.clear(nameInput)
    await user.type(nameInput, 'renamed')

    expect(select.value).toBe('anthropic')
    expect(screen.getByTestId('current-provider').textContent).toBe('anthropic')
  })

  it('does not show a deprecation warning for a supported provider', () => {
    render(<Harness initialProvider="claude" />)
    expect(screen.queryByText(/is deprecated, disabled for new configs/i)).not.toBeInTheDocument()
    const [select] = screen.getAllByRole('combobox') as HTMLSelectElement[]
    expect(select.value).toBe('claude')
  })
})

// Env values arrive from the server pre-masked as "***" (see
// backend/internal/api/handlers/provider_env.go) — the form never sees a
// real secret. These tests cover the masked-key hint and the ✕ delete
// affordance added alongside that write-only contract.

function EnvHarness({ initialEnv }: { initialEnv: string }) {
  const [form, setForm] = useState<FormState>({
    name: 'has-secrets',
    provider: 'claude',
    model: 'sonnet',
    env: initialEnv,
  })
  const selected: ProviderConfig = {
    id: 'pc1',
    name: form.name,
    provider: form.provider,
    model: form.model,
    env: form.env,
    created_at: new Date().toISOString(),
    updated_at: new Date().toISOString(),
  }
  return (
    <div>
      <div data-testid="current-env">{form.env}</div>
      <ProviderConfigForm
        selected={selected}
        form={form}
        setForm={setForm}
        modelList={null}
        fetchingModels={false}
        saving={false}
        deleting={false}
        onSave={() => {}}
        onDelete={() => {}}
      />
    </div>
  )
}

describe('ProviderConfigForm — masked env values', () => {
  it('renders the masked-value hint and a key chip for a "***" value', () => {
    render(<EnvHarness initialEnv='{"ANTHROPIC_API_KEY":"***"}' />)

    expect(screen.getByText(/values shown as \*\*\* are set but hidden/i)).toBeInTheDocument()
    expect(screen.getByText('ANTHROPIC_API_KEY')).toBeInTheDocument()
  })

  it('does not render the hint or any chip when there are no masked values', () => {
    render(<EnvHarness initialEnv='{"FOO":"plain-new-value"}' />)

    expect(screen.queryByText(/values shown as \*\*\* are set but hidden/i)).not.toBeInTheDocument()
    expect(screen.queryByText('FOO')).not.toBeInTheDocument()
  })

  it('removing a masked key via the ✕ affordance deletes it from form.env', async () => {
    const user = userEvent.setup()
    render(<EnvHarness initialEnv='{"ANTHROPIC_API_KEY":"***","OTHER_KEY":"***"}' />)

    await user.click(screen.getByLabelText('Remove ANTHROPIC_API_KEY'))

    const parsed = JSON.parse(screen.getByTestId('current-env').textContent ?? '{}')
    expect(parsed).toEqual({ OTHER_KEY: '***' })
  })
})
