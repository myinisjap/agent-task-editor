import TabbedPage from '../components/shared/TabbedPage'

// Workflow and Agents as two tabs of one "Pipeline" destination. A later
// change is expected to replace this with a graph view.
const tabs = [
  { to: '/pipeline/workflow', label: 'Workflow' },
  { to: '/pipeline/agents', label: 'Agents' },
]

export default function PipelinePage() {
  return <TabbedPage testId="pipeline-page" tabs={tabs} />
}
