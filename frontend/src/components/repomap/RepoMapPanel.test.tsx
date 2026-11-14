import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import RepoMapPanel, { stalenessText } from './RepoMapPanel'

const m = { map: vi.fn(), runs: vi.fn(), setCfg: vi.fn(), gen: vi.fn(), providers: vi.fn() }
vi.mock('../../api/client', () => ({
  api: {
    repos: {
      map: (...a: unknown[]) => m.map(...a),
      mapRuns: (...a: unknown[]) => m.runs(...a),
      setMapConfig: (...a: unknown[]) => m.setCfg(...a),
      generateMap: (...a: unknown[]) => m.gen(...a),
    },
    providerConfigs: { list: (...a: unknown[]) => m.providers(...a) },
  },
}))

function mapOf(o: Record<string, unknown> = {}) {
  return {
    config: { enabled: true, provider_config_id: 'pc1', refresh_interval_hours: 168, min_commits_behind: 20 },
    pages: [{ slug: 'overview', title: 'Overview', body: 'An **app**.', commit_sha: 'abcdef123456' }],
    commit_sha: 'abcdef123456', generated_at: null, head_sha: 'x', commits_behind: 7, layout_changed: false,
    stale: false, running: false, last_run: null, ...o,
  }
}

describe('RepoMapPanel', () => {
  beforeEach(() => {
    Object.values(m).forEach((f) => f.mockReset())
    m.runs.mockResolvedValue([])
    m.providers.mockResolvedValue([{ id: 'pc1', name: 'Claude' }])
    m.map.mockResolvedValue(mapOf())
  })

  it('renders pages as markdown with the staleness line', async () => {
    render(<RepoMapPanel repoId="r1" />)
    expect(await screen.findByText('Overview')).toBeInTheDocument()
    expect(screen.getByText('app').tagName).toBe('STRONG')
    expect(screen.getByText(/generated at abcdef12, 7 commits behind/)).toBeInTheDocument()
  })

  it('refreshes on demand', async () => {
    m.gen.mockResolvedValue({})
    render(<RepoMapPanel repoId="r1" />)
    await userEvent.click(await screen.findByRole('button', { name: 'Refresh' }))
    await waitFor(() => expect(m.gen).toHaveBeenCalledWith('r1'))
  })

  it('shows the off state and blocks generation while disabled', async () => {
    m.map.mockResolvedValue(mapOf({ config: { enabled: false, provider_config_id: null, refresh_interval_hours: 168, min_commits_behind: 20 }, pages: [], commit_sha: '' }))
    render(<RepoMapPanel repoId="r1" />)
    expect(await screen.findByText(/^Off\./)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Generate' })).toBeDisabled()
    expect(screen.getByText('No map generated yet.')).toBeInTheDocument()
  })

  it('formats staleness', () => {
    expect(stalenessText(mapOf({ commits_behind: 1 }) as any)).toBe('generated at abcdef12, 1 commit behind')
    expect(stalenessText(mapOf({ commits_behind: 0 }) as any)).toContain('up to date')
    expect(stalenessText(mapOf({ commits_behind: null }) as any)).toContain('unknown')
  })
})
