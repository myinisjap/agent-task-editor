// PricingSettingsPage tests: loads the current pricing table, validates
// client-side before saving (empty/duplicate model, negative price), and
// saves valid edits (including add/remove rows) via api.modelPricing.update.
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import PricingSettingsPage from './PricingSettingsPage'

vi.mock('../api/client', async () => {
  const actual = await vi.importActual<typeof import('../api/client')>('../api/client')
  return {
    ...actual,
    api: {
      modelPricing: {
        list: vi.fn(),
        update: vi.fn(),
      },
    },
  }
})

import { api } from '../api/client'

const SEEDED = [
  { model: 'gpt-4o', input_per_1m: 2.5, output_per_1m: 10, updated_at: '2026-01-01T00:00:00Z' },
]

beforeEach(() => {
  vi.clearAllMocks()
  ;(api.modelPricing.list as ReturnType<typeof vi.fn>).mockResolvedValue(SEEDED)
  ;(api.modelPricing.update as ReturnType<typeof vi.fn>).mockImplementation(
    async (rows: { model: string; input_per_1m: number; output_per_1m: number }[]) =>
      rows.map((r) => ({ ...r, updated_at: '2026-01-02T00:00:00Z' })),
  )
})

describe('PricingSettingsPage', () => {
  it('loads and displays the current pricing table', async () => {
    render(<PricingSettingsPage />)

    expect(await screen.findByDisplayValue('gpt-4o')).toBeInTheDocument()
    expect(screen.getByDisplayValue('2.5')).toBeInTheDocument()
    expect(screen.getByDisplayValue('10')).toBeInTheDocument()
  })

  it('rejects an empty model without calling the API', async () => {
    const user = userEvent.setup()
    render(<PricingSettingsPage />)

    const modelInput = await screen.findByDisplayValue('gpt-4o')
    await user.clear(modelInput)
    await user.click(screen.getByRole('button', { name: /Save pricing table/i }))

    expect(await screen.findByText(/must not be empty/i)).toBeInTheDocument()
    expect(api.modelPricing.update).not.toHaveBeenCalled()
  })

  it('rejects a negative price without calling the API', async () => {
    const user = userEvent.setup()
    render(<PricingSettingsPage />)

    const inputPriceField = await screen.findByDisplayValue('2.5')
    await user.clear(inputPriceField)
    await user.type(inputPriceField, '-1')
    await user.click(screen.getByRole('button', { name: /Save pricing table/i }))

    expect(await screen.findByText(/must be a number >= 0/i)).toBeInTheDocument()
    expect(api.modelPricing.update).not.toHaveBeenCalled()
  })

  it('rejects duplicate models without calling the API', async () => {
    const user = userEvent.setup()
    render(<PricingSettingsPage />)

    await screen.findByDisplayValue('gpt-4o')
    await user.click(screen.getByRole('button', { name: /\+ Add row/i }))
    const modelInputs = screen.getAllByPlaceholderText(/e\.g\. claude-sonnet-4-5/i)
    await user.type(modelInputs[1], 'gpt-4o')
    await user.click(screen.getByRole('button', { name: /Save pricing table/i }))

    expect(await screen.findByText(/Duplicate model/i)).toBeInTheDocument()
    expect(api.modelPricing.update).not.toHaveBeenCalled()
  })

  it('adds a row and saves the full replaced table via the API', async () => {
    const user = userEvent.setup()
    render(<PricingSettingsPage />)

    await screen.findByDisplayValue('gpt-4o')
    await user.click(screen.getByRole('button', { name: /\+ Add row/i }))
    const modelInputs = screen.getAllByPlaceholderText(/e\.g\. claude-sonnet-4-5/i)
    await user.type(modelInputs[1], 'my-custom-model')

    await user.click(screen.getByRole('button', { name: /Save pricing table/i }))

    await waitFor(() =>
      expect(api.modelPricing.update).toHaveBeenCalledWith([
        { model: 'gpt-4o', input_per_1m: 2.5, output_per_1m: 10 },
        { model: 'my-custom-model', input_per_1m: 0, output_per_1m: 0 },
      ]),
    )
    expect(await screen.findByText(/Pricing table saved/i)).toBeInTheDocument()
  })

  it('removes a row and saves without it', async () => {
    const user = userEvent.setup()
    render(<PricingSettingsPage />)

    await screen.findByDisplayValue('gpt-4o')
    await user.click(screen.getByRole('button', { name: /Remove gpt-4o/i }))
    await user.click(screen.getByRole('button', { name: /Save pricing table/i }))

    await waitFor(() => expect(api.modelPricing.update).toHaveBeenCalledWith([]))
  })
})
