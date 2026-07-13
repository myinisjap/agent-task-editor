// Shared helper for building a vi.fn()-backed partial mock of `api` (see
// ../api/client.ts). Individual test files still call
// `vi.mock('../../api/client', ...)` (or the appropriate relative path)
// themselves — vi.mock is hoisted and must reference a factory in the same
// file — but can use this factory to avoid re-typing every method's default
// resolved value across TaskBoard/BoardPage/TaskDetailPage tests.
import { vi } from 'vitest'

export function makeApiMock() {
  return {
    tasks: {
      list: vi.fn().mockResolvedValue({ items: [], nextCursor: null }),
      get: vi.fn(),
      create: vi.fn(),
      update: vi.fn(),
      delete: vi.fn().mockResolvedValue(undefined),
      moveLabel: vi.fn(),
      approve: vi.fn(),
      reject: vi.fn(),
      updateNotes: vi.fn(),
      rerun: vi.fn(),
      diff: vi.fn(),
      prUrl: vi.fn(),
      createPR: vi.fn(),
      githubStatus: vi.fn(),
      updateGitState: vi.fn(),
      setPaused: vi.fn(),
      setArchived: vi.fn(),
      bulk: vi.fn().mockResolvedValue({ results: [] }),
      dependencies: vi.fn().mockResolvedValue({ blocked_by: [], blocking: [], blocked_by_count: 0, blocking_count: 0 }),
      addDependency: vi.fn(),
      removeDependency: vi.fn(),
      subtasks: vi.fn().mockResolvedValue([]),
      createSubtask: vi.fn(),
      reviewComments: vi.fn().mockResolvedValue([]),
      addReviewComment: vi.fn(),
      updateReviewComment: vi.fn(),
      deleteReviewComment: vi.fn(),
      runs: vi.fn().mockResolvedValue([]),
      getRun: vi.fn(),
      listLabelHistory: vi.fn().mockResolvedValue([]),
      cancelRun: vi.fn(),
      replyRun: vi.fn(),
      runLogs: vi.fn().mockResolvedValue({ items: [], hasMore: false, prevCursor: null }),
    },
    workflows: {
      list: vi.fn().mockResolvedValue([]),
      get: vi.fn(),
      create: vi.fn(),
      update: vi.fn(),
      delete: vi.fn(),
      exportYaml: vi.fn(),
      updateYaml: vi.fn(),
      importYaml: vi.fn(),
    },
    agents: {
      list: vi.fn().mockResolvedValue([]),
      get: vi.fn(),
      create: vi.fn(),
      update: vi.fn(),
      delete: vi.fn(),
      models: vi.fn(),
      claudeOptions: vi.fn(),
    },
    repos: {
      list: vi.fn().mockResolvedValue([]),
      get: vi.fn(),
      create: vi.fn(),
      update: vi.fn(),
      delete: vi.fn(),
      tree: vi.fn(),
    },
    templates: {
      list: vi.fn().mockResolvedValue([]),
      create: vi.fn(),
      update: vi.fn(),
      delete: vi.fn(),
    },
    dashboard: {
      get: vi.fn(),
      costByTask: vi.fn().mockResolvedValue([]),
    },
    github: {
      authStatus: vi.fn().mockResolvedValue({ authed: true, note: '' }),
    },
    health: {
      providers: vi.fn().mockResolvedValue({ checks: [] }),
    },
    backup: {
      url: vi.fn(),
    },
  }
}

export function makeWsClientMock() {
  return {
    connect: vi.fn().mockResolvedValue(undefined),
    on: vi.fn(() => () => {}),
    subscribeTask: vi.fn(),
    unsubscribeTask: vi.fn(),
  }
}
