import TabbedPage from '../components/shared/TabbedPage'

// Overview (graph of who works each stage), Workflow and Agents as tabs of one
// "Pipeline" destination.
const tabs = [
  { to: '/pipeline/overview', label: 'Overview' },
  { to: '/pipeline/workflow', label: 'Workflow' },
  { to: '/pipeline/agents', label: 'Agents' },
]

export default function PipelinePage() {
  return <TabbedPage testId="pipeline-page" tabs={tabs} />
}
