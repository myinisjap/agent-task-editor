// TaskBoard drag-to-move + rollback tests.
//
// @dnd-kit's sensors listen for real pointer events (pointerdown/move/up);
// this simulates a drag by firing PointerEvent-backed fireEvent calls on the
// draggable TaskCard and the droppable TaskColumn drop zone, following
// dnd-kit's documented Testing-Library recipe. jsdom needs a PointerEvent
// polyfill and stubbed pointer-capture methods for this to work at all — see
// src/test/setup.ts. getBoundingClientRect is stubbed per-element below so
// dnd-kit's collision detection (rectIntersection) can resolve a drop target.
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, fireEvent } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import TaskBoard from './TaskBoard'
import type { Task, WorkflowLabel } from '../../api/client'
import { useTasksStore } from '../../stores/tasks'

const moveLabelMock = vi.fn()

vi.mock('../../api/client', async () => {
  const actual = await vi.importActual<typeof import('../../api/client')>('../../api/client')
  return {
    ...actual,
    api: {
      tasks: {
        moveLabel: (...args: unknown[]) => moveLabelMock(...args),
        setPaused: vi.fn(),
        setArchived: vi.fn(),
        update: vi.fn(),
        delete: vi.fn(),
      },
    },
  }
})

function label(name: string, sortOrder: number): WorkflowLabel {
  return {
    id: name,
    workflow_id: 'wf',
    name,
    color: '#000',
    sort_order: sortOrder,
    agent_ignore: 0,
    is_terminal: 0,
    create_pr: 0,
  }
}

function task(overrides: Partial<Task> = {}): Task {
  return {
    id: 'task-1',
    title: 'Move me',
    description: '',
    type: 'feature',
    label: 'todo',
    repo_id: 'r1',
    workflow_id: 'wf',
    created_at: new Date().toISOString(),
    updated_at: new Date().toISOString(),
    ...overrides,
  }
}

// Stub an element's bounding rect so dnd-kit's rectIntersection collision
// detection has real geometry to compare against.
function stubRect(el: Element, rect: { left: number; top: number; width: number; height: number }) {
  const domRect = {
    ...rect,
    right: rect.left + rect.width,
    bottom: rect.top + rect.height,
    x: rect.left,
    y: rect.top,
    toJSON: () => rect,
  } as DOMRect
  vi.spyOn(el, 'getBoundingClientRect').mockReturnValue(domRect)
}

// Find the draggable card's outer element (the one with the dnd-kit pointer
// listeners) by walking up from its title text.
function findCardEl(title: string): HTMLElement {
  const titleEl = screen.getByText(title)
  const cardEl = titleEl.closest('.group') as HTMLElement | null
  if (!cardEl) throw new Error(`could not find card container for "${title}"`)
  return cardEl
}

// Find a column's droppable drop-zone element by its label header text.
function findColumnDropZone(labelName: string): HTMLElement {
  const header = screen.getByText(labelName)
  const columnRoot = header.closest('.flex.flex-col.shrink-0') as HTMLElement | null
  if (!columnRoot) throw new Error(`could not find column for "${labelName}"`)
  const dropZone = columnRoot.querySelector('.flex-1.flex.flex-col') as HTMLElement | null
  if (!dropZone) throw new Error(`could not find drop zone for "${labelName}"`)
  return dropZone
}

async function dragCardToColumn(cardTitle: string, targetLabel: string) {
  const cardEl = findCardEl(cardTitle)
  const dropZone = findColumnDropZone(targetLabel)

  // Card starts inside the "from" column, drop zone is the "to" column.
  stubRect(cardEl, { left: 10, top: 10, width: 200, height: 80 })
  stubRect(dropZone, { left: 400, top: 0, width: 300, height: 600 })

  // TaskBoard configures @dnd-kit's MouseSensor (activationConstraint:
  // 5px) for pointer-driven drags: it activates via the React
  // `onMouseDown` handler, then listens for native `mousemove`/`mouseup`
  // events on the document to track and end the drag.
  fireEvent.mouseDown(cardEl, { clientX: 20, clientY: 20, button: 0 })
  // MouseSensor requires 5px movement before a drag "activates".
  fireEvent.mouseMove(document, { clientX: 30, clientY: 20 })
  // Move over the destination column so dnd-kit's collision detection picks
  // it as the active droppable.
  fireEvent.mouseMove(document, { clientX: 500, clientY: 100 })
  fireEvent.mouseUp(document, { clientX: 500, clientY: 100 })
}

describe('TaskBoard drag-to-move', () => {
  const labels = [label('todo', 0), label('doing', 1), label('done', 2)]

  beforeEach(() => {
    moveLabelMock.mockReset()
    useTasksStore.setState({ tasks: [task()], loading: false, error: null })
  })

  it('dragging a card to another column calls api.tasks.moveLabel', async () => {
    moveLabelMock.mockResolvedValue(task({ label: 'doing' }))

    render(
      <MemoryRouter>
        <TaskBoard
          labels={labels}
          tasks={[task()]}
          runningTaskIds={new Set()}
        />
      </MemoryRouter>,
    )

    await dragCardToColumn('Move me', 'doing')

    await waitFor(() => {
      expect(moveLabelMock).toHaveBeenCalledWith('task-1', 'doing')
    })
  })

  it('rolls back the optimistic move when moveLabel rejects', async () => {
    moveLabelMock.mockRejectedValue(new Error('server rejected'))
    useTasksStore.setState({ tasks: [task()], loading: false, error: null })

    function Wrapper() {
      const tasks = useTasksStore((s) => s.tasks)
      return <TaskBoard labels={labels} tasks={tasks} runningTaskIds={new Set()} />
    }

    render(
      <MemoryRouter>
        <Wrapper />
      </MemoryRouter>,
    )

    await dragCardToColumn('Move me', 'doing')

    await waitFor(() => {
      expect(moveLabelMock).toHaveBeenCalledWith('task-1', 'doing')
    })

    // After the optimistic upsert, the store briefly reflects 'doing'; once
    // the rejected promise settles, TaskBoard's handleDragEnd snaps it back.
    await waitFor(() => {
      expect(useTasksStore.getState().tasks[0].label).toBe('todo')
    })
  })

  it('confirms before moving a blocked task into an agent-triggerable column, and skips the move if declined', async () => {
    const blockedTask = task({ blocked_by_count: 2 })
    useTasksStore.setState({ tasks: [blockedTask], loading: false, error: null })
    const confirmSpy = vi.spyOn(window, 'confirm').mockReturnValue(false)

    const transitions = [
      { id: 't1', workflow_id: 'wf', from_label: 'doing', to_label: 'done', trigger_type: 'agent' as const },
    ]

    render(
      <MemoryRouter>
        <TaskBoard
          labels={labels}
          tasks={[blockedTask]}
          runningTaskIds={new Set()}
          transitions={transitions}
        />
      </MemoryRouter>,
    )

    await dragCardToColumn('Move me', 'doing')

    await waitFor(() => {
      expect(confirmSpy).toHaveBeenCalled()
    })
    expect(moveLabelMock).not.toHaveBeenCalled()

    confirmSpy.mockRestore()
  })
})
