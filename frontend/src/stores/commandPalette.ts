import { create } from 'zustand'

export interface TaskCommand {
  id: string
  label: string
  group: 'Task'
  keywords?: string
  run: () => void | Promise<void>
}

export interface TaskCommandContext {
  taskId: string
  title: string
  commands: TaskCommand[]
}

interface CommandPaletteState {
  open: boolean
  setOpen: (v: boolean) => void
  toggle: () => void
  sheetOpen: boolean
  setSheetOpen: (v: boolean) => void
  newTaskOpen: boolean
  setNewTaskOpen: (v: boolean) => void
  boardSearchFocusRequested: number
  requestBoardSearchFocus: () => void
  taskContext: TaskCommandContext | null
  setTaskContext: (ctx: TaskCommandContext | null) => void
}

export const useCommandPaletteStore = create<CommandPaletteState>((set) => ({
  open: false,
  setOpen: (open) => set({ open }),
  toggle: () => set((s) => ({ open: !s.open })),
  sheetOpen: false,
  setSheetOpen: (sheetOpen) => set({ sheetOpen }),
  newTaskOpen: false,
  setNewTaskOpen: (newTaskOpen) => set({ newTaskOpen }),
  boardSearchFocusRequested: 0,
  requestBoardSearchFocus: () => set((s) => ({ boardSearchFocusRequested: s.boardSearchFocusRequested + 1 })),
  taskContext: null,
  setTaskContext: (taskContext) => set({ taskContext }),
}))
