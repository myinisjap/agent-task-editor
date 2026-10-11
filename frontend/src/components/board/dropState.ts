/** Drag feedback for a board column while a card is being dragged. */
export type DropState = 'legal' | 'illegal'

export function dropStateClass(state: DropState | undefined): string {
  if (state === 'legal') return 'ring-2 ring-indigo-500/60'
  if (state === 'illegal') return 'opacity-40'
  return ''
}
