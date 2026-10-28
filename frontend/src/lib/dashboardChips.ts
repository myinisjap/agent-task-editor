import { labelDisplayName, labelDisplayNameByName } from './labelDisplay'
import type { Workflow } from '../api/client'

const DEFAULT_LABEL_COLOR = '#6B7280'

export type ChipRow = { key: string; title?: string; chips: { key: string; name: string; color: string; count: number }[] }

// buildChipRows turns dashboard counts into rows of label chips colored from
// each label's configured workflow color. With more than one workflow, counts
// come from workflow_label_counts and each workflow gets its own titled row;
// otherwise one untitled row. Labels no longer in any workflow fall back to a
// neutral color.
export function buildChipRows(
  workflows: Workflow[],
  labelCounts: Record<string, number>,
  workflowLabelCounts?: Record<string, Record<string, number>>,
): ChipRow[] {
  const colorOf = (wf: Workflow | undefined, name: string) =>
    wf?.labels.find((l) => l.name === name)?.color || DEFAULT_LABEL_COLOR

  if (workflows.length > 1 && workflowLabelCounts) {
    const rows: ChipRow[] = []
    for (const wf of workflows) {
      const counts = workflowLabelCounts[wf.id] ?? {}
      const ordered = [...wf.labels].sort((a, b) => a.sort_order - b.sort_order)
      const chips = ordered
        .filter((l) => (counts[l.name] ?? 0) > 0)
        .map((l) => ({ key: l.id, name: labelDisplayName(l), color: l.color || DEFAULT_LABEL_COLOR, count: counts[l.name] }))
      if (chips.length > 0) rows.push({ key: wf.id, title: wf.name, chips })
    }
    return rows
  }

  const wf = workflows[0]
  return [{
    key: 'all',
    chips: Object.entries(labelCounts).map(([name, count]) => ({ key: name, name: labelDisplayNameByName(name, wf?.labels), color: colorOf(wf, name), count })),
  }]
}
