// Bucketing logic for the fun "factory" dashboard visualization, kept out of
// TaskFactory.tsx so that file only exports a component (Fast Refresh rule).
import type { Workflow } from '../api/client'

const DEFAULT_LABEL_NAMES = ['agent-review', 'done', 'not_ready', 'plan', 'review', 'review-plan', 'testing', 'work']

export type Buckets = { notReady: string[]; agentWorking: string[]; waitingHuman: string[] }

export function bucketize(workflow: Workflow): Buckets | null {
  const names = workflow.labels.map((l) => l.name).sort()
  if (names.length === DEFAULT_LABEL_NAMES.length && names.every((n, i) => n === DEFAULT_LABEL_NAMES[i])) {
    return null
  }

  const notReady: string[] = []
  const agentWorking: string[] = []
  const waitingHuman: string[] = []

  const agentFromLabels = new Set(
    workflow.transitions
      .filter((t) => t.trigger_type === 'agent' || t.trigger_type === 'both')
      .map((t) => t.from_label),
  )

  for (const label of workflow.labels) {
    if (label.agent_ignore === 1) {
      notReady.push(label.name)
    } else if (agentFromLabels.has(label.name)) {
      agentWorking.push(label.name)
    } else if (label.is_terminal !== 1) {
      waitingHuman.push(label.name)
    }
    // terminal labels that fall through are excluded from every bucket
  }

  return { notReady, agentWorking, waitingHuman }
}

// ponytail: assert-based self-check for the only non-trivial logic here;
// full coverage lives in the colocated TaskFactory.test.tsx.
function selfCheck() {
  const defaultWf: Workflow = {
    id: 'wf', name: '', description: '', created_at: '', updated_at: '',
    labels: DEFAULT_LABEL_NAMES.map((name, i) => ({
      id: name, workflow_id: 'wf', name, color: '#000', sort_order: i, agent_ignore: 0, is_terminal: name === 'done' ? 1 : 0,
    })),
    transitions: [],
  }
  console.assert(bucketize(defaultWf) === null, 'default workflow should bucketize to null')
}
if (import.meta.env.DEV) selfCheck()
