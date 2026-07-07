import ChipPicker from '../shared/ChipPicker'

export default function LabelPicker({ selected, available, onChange }: {
  selected: string[]
  available: string[]
  onChange: (labels: string[]) => void
}) {
  return (
    <ChipPicker
      selected={selected}
      available={available.map((name) => ({ value: name, label: name }))}
      onChange={onChange}
      emptyMessage="No workflow labels found. Configure a workflow first."
    />
  )
}
