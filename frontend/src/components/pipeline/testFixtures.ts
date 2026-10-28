import type { Workflow } from '../../api/client'

const lbl = (name: string, i: number, extra: Record<string, unknown> = {}) =>
  ({ id: name, workflow_id: 'w1', name, color: '#6366f1', sort_order: i, agent_ignore: 0, is_terminal: 0, create_pr: 0, wip_limit_hard: 0, verify_mode: 'inherit', verify_command: '', ...extra })
const tr = (from: string, to: string, trigger: string, path = 'success') =>
  ({ id: `${from}-${to}-${path}`, workflow_id: 'w1', from_label: from, to_label: to, trigger_type: trigger, path })

/** plan → review-plan (gate, no diff) → work → review (gate) → done (opens PR). */
export const gateWorkflow = (reviewAutonomy?: Record<string, unknown>): Workflow =>
  ({
    id: 'w1', name: 'Main', description: '', updated_at: '2025-01-01T00:00:00Z',
    labels: [
      lbl('plan', 0), lbl('review-plan', 1), lbl('work', 2),
      lbl('review', 3, reviewAutonomy ? { autonomy: reviewAutonomy } : {}),
      lbl('done', 4, { is_terminal: 1, create_pr: 1 }),
    ],
    transitions: [
      tr('plan', 'review-plan', 'agent'), tr('review-plan', 'work', 'human'),
      tr('work', 'review', 'agent'), tr('review', 'done', 'human'), tr('review', 'work', 'human', 'failure'),
    ],
  }) as unknown as Workflow

export const backtestResult = {
  total: 3, evaluated: 3, unknown: 0, would_skip: 2, would_ask: 1, rejected: 1, escaped: 0,
  truncated: false, timed_out: false,
  passes: [
    { task_id: 't1', title: 'One', at: '2025-01-01T00:00:00Z', outcome: 'approved', flags: [], would_skip: true, unknown: false },
    { task_id: 't2', title: 'Two', at: '2025-01-02T00:00:00Z', outcome: 'approved', flags: ['auth_or_security'], would_skip: false, unknown: false },
    { task_id: 't3', title: 'Three', at: '2025-01-03T00:00:00Z', outcome: 'rejected', flags: [], would_skip: false, unknown: false },
  ],
}
