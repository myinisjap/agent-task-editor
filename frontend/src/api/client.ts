import { authHeaders, notifyUnauthorized } from './authToken'
import type { components } from './types'

// ----- API schema types -----
//
// These are re-exported straight from the generated OpenAPI types
// (src/api/types.ts, produced by `npm run gen:api` from the root
// openapi.yaml — do NOT hand-edit it). openapi.yaml is the single source of
// truth for the wire shapes; adjust a type by editing the spec and
// regenerating, not by editing here. Consumers keep importing these names
// from './client' unchanged.
type Schemas = components['schemas']

export type ProviderConfig = Schemas['ProviderConfig']
export type ChatSession = Schemas['ChatSession']
export type Task = Schemas['Task']
export type DependencyEdge = Schemas['DependencyEdge']
export type TaskDependencies = Schemas['TaskDependencies']
export type TaskTemplate = Schemas['TaskTemplate']
export type TaskSchedule = Schemas['TaskSchedule']
export type BackupSettings = Schemas['BackupSettings']
export type LogRetentionSettings = Schemas['LogRetentionSettings']
export type ModelPricing = Schemas['ModelPricing']
export type WorkflowLabel = Schemas['WorkflowLabel']
export type WorkflowTransition = Schemas['WorkflowTransition']
export type Workflow = Schemas['Workflow']
export type AgentConfig = Schemas['AgentConfig']
export type ModelList = Schemas['ModelList']
export type ClaudeOptions = Schemas['ClaudeOptions']
export type Repo = Schemas['Repo']
export type ReviewComment = Schemas['ReviewComment']
export type AgentRun = Schemas['AgentRun']
// Named *Entry historically; the schema is TaskLabelHistory.
export type TaskLabelHistoryEntry = Schemas['TaskLabelHistory']
export type AgentLog = Schemas['AgentLog']
export type Dashboard = Schemas['Dashboard']
export type ProviderCheck = Schemas['ProviderCheck']

// Narrowed alias for the ProviderCheck.status enum, kept for callers that
// annotate a status value directly.
export type ProviderCheckStatus = ProviderCheck['status']

// ----- App-only helper types (not part of openapi.yaml) -----

// Cursor-paginated result: a page of items plus the cursor for the next page
// (null when the list is exhausted).
export type Page<T> = { items: T[]; nextCursor: string | null }

// Optional filters for GET /tasks. `q` is a case-insensitive substring match
// against title/description; the rest are exact matches. `archived` defaults
// to hiding archived tasks; 'only' returns just archived, 'all' everything.
export type TaskFilters = {
  q?: string
  label?: string
  repo_id?: string
  type?: string
  git_state?: string
  archived?: 'all' | 'only'
}

// Action applied by POST /tasks/bulk to every id in the request.
export type BulkAction = 'move' | 'pause' | 'resume' | 'archive' | 'unarchive'

export type BulkResult = { id: string; ok: boolean; error?: string }

// TaskCost is a single row of the { task_id, cost_usd } cost rollup returned
// by GET /dashboard/cost-by-task, used by the board page to compute the
// total cost of the currently-selected filter. Unlike Dashboard.cost_by_task
// this endpoint returns every task (no top-N cap, no title) since the board
// needs a cost for every visible task, not just the most expensive ones.
export type TaskCost = { task_id: string; input_tokens: number; output_tokens: number; cost_usd: number }

// BASE is the API root, BASE_URL-prefixed so the app works when served from
// a non-root base (e.g. the production '/tasks/' base set in
// vite.config.ts). Exported so other modules that need to build API-relative
// URLs (e.g. TaskHeader's attachment links, see #145) share this single
// source of truth instead of re-deriving it (and risking drift).
export const BASE = `${import.meta.env.BASE_URL.replace(/\/$/, '')}/api/v1`

// authedRawFetch is a thin wrapper around fetch() that merges in the
// Authorization header from the runtime token (see authToken.ts) and, on a
// 401 response, clears the stored token and notifies ApiTokenGate so it can
// prompt for a new one. It does not throw on non-2xx — callers handle that
// themselves (request()/requestWithHeaders() below, or any other raw fetch()
// call site that needs auth, e.g. HealthPage's backup download or
// WorkflowPage's YAML export).
export async function authedRawFetch(url: string, init?: RequestInit): Promise<Response> {
  const res = await fetch(url, {
    ...init,
    headers: { ...authHeaders(), ...init?.headers },
  })
  if (res.status === 401) notifyUnauthorized()
  return res
}

async function request<T>(path: string, init?: RequestInit & { isFormData?: boolean }): Promise<T> {
  const headers: Record<string, string> = { ...authHeaders() }
  if (!init?.isFormData) {
    headers['Content-Type'] = 'application/json'
  }
  // `headers` must be spread AFTER `...init` — init still carries its own
  // (unmerged) `headers` key, which would otherwise clobber the merged
  // object below if spread last. This was a latent bug even before
  // authHeaders() existed (it happened to be unobservable when the only
  // thing at stake was Content-Type, since callers that pass a custom
  // `init.headers` were always overriding an equivalent single-key
  // default) — see #138, where it meant a caller-supplied `init.headers`
  // (e.g. workflows.updateYaml/importYaml's 'application/yaml'
  // Content-Type) silently dropped the Authorization header entirely.
  const res = await authedRawFetch(`${BASE}${path}`, {
    ...init,
    headers: { ...headers, ...init?.headers },
  })
  if (!res.ok) {
    const err = await res.json().catch(() => ({ error: res.statusText }))
    throw new Error(err.error ?? res.statusText)
  }
  if (res.status === 204) return undefined as T
  return res.json()
}

// requestWithHeaders is like request but also surfaces the response headers, so
// callers can read pagination cursors (X-Next-Cursor / X-Prev-Cursor /
// X-Has-More) that the list endpoints return alongside the array body.
async function requestWithHeaders<T>(path: string, init?: RequestInit): Promise<{ data: T; headers: Headers }> {
  const res = await authedRawFetch(`${BASE}${path}`, {
    ...init,
    headers: { 'Content-Type': 'application/json', ...init?.headers },
  })
  if (!res.ok) {
    const err = await res.json().catch(() => ({ error: res.statusText }))
    throw new Error(err.error ?? res.statusText)
  }
  const data = (res.status === 204 ? undefined : await res.json()) as T
  return { data, headers: res.headers }
}

export const api = {
  tasks: {
    // list returns a single page of tasks (newest first). Pass `after` (a
    // cursor from a previous page's nextCursor) and `limit` to page through.
    // The nextCursor is read from the X-Next-Cursor response header; it is null
    // once no more tasks remain.
    list: async (filters?: TaskFilters, opts?: { after?: string; limit?: number }): Promise<Page<Task>> => {
      const params = new URLSearchParams()
      for (const [key, value] of Object.entries(filters ?? {})) {
        if (value) params.set(key, String(value))
      }
      if (opts?.after) params.set('after', opts.after)
      if (opts?.limit) params.set('limit', String(opts.limit))
      const qs = params.toString()
      const { data, headers } = await requestWithHeaders<Task[]>(`/tasks${qs ? `?${qs}` : ''}`)
      return { items: data ?? [], nextCursor: headers.get('X-Next-Cursor') || null }
    },
    get: (id: string) => request<Task>(`/tasks/${id}`),
    create: (body: FormData | { title: string; description?: string; type?: string; repo_id: string; workflow_id: string; priority?: number }) => {
      if (body instanceof FormData) {
        return request<Task>('/tasks', { method: 'POST', body, isFormData: true })
      }
      return request<Task>('/tasks', { method: 'POST', body: JSON.stringify(body) })
    },
    update: (id: string, body: { title?: string; description?: string; type?: string; repo_id?: string; max_cost_usd?: number; priority?: number }) =>
      request<Task>(`/tasks/${id}`, { method: 'PATCH', body: JSON.stringify(body) }),
    delete: (id: string) => request<void>(`/tasks/${id}`, { method: 'DELETE' }),
    moveLabel: (id: string, to_label: string, note?: string) =>
      request<Task>(`/tasks/${id}/label`, { method: 'PATCH', body: JSON.stringify({ to_label, note }) }),
    approve: (id: string, note?: string) =>
      request<Task>(`/tasks/${id}/approve`, { method: 'POST', body: JSON.stringify({ note }) }),
   reject: (id: string, note: string, to_label?: string) =>
       request<Task>(`/tasks/${id}/reject`, { method: 'POST', body: JSON.stringify({ note, to_label }) }),
     updateNotes: (id: string, notes: string, append = false) =>
       request<Task>(`/tasks/${id}/notes`, { method: 'PATCH', body: JSON.stringify({ notes, append }) }),
     rerun: (id: string) => request<void>(`/tasks/${id}/rerun`, { method: 'POST' }),
    diff: (id: string) => request<{ branch: string; diff: string }>(`/tasks/${id}/diff`),
    prUrl: (id: string) => request<{ url: string }>(`/tasks/${id}/pr-url`),
    createPR: (id: string) =>
      request<{ pr_url: string; git_state: Task['git_state'] }>(`/tasks/${id}/pr`, { method: 'POST' }),
    githubStatus: (id: string) =>
      request<{ git_state: Task['git_state']; pr_url: string; error?: string }>(`/tasks/${id}/github-status`),
    updateGitState: (id: string, git_state: string) =>
      request<Task>(`/tasks/${id}/git-state`, { method: 'PATCH', body: JSON.stringify({ git_state }) }),
    setPaused: (id: string, paused: boolean) =>
      request<Task>(`/tasks/${id}/pause`, { method: 'PATCH', body: JSON.stringify({ paused }) }),
    setArchived: (id: string, archived: boolean) =>
      request<Task>(`/tasks/${id}/archive`, { method: 'PATCH', body: JSON.stringify({ archived }) }),
    bulk: (ids: string[], action: BulkAction, opts?: { to_label?: string; note?: string }) =>
      request<{ results: BulkResult[] }>('/tasks/bulk', {
        method: 'POST',
        body: JSON.stringify({ ids, action, ...opts }),
      }),
    // Peer dependencies (dispatch gate). dependencies() lists both directions;
    // addDependency() gates this task behind another (409 on cycle/duplicate,
    // 400 on self/cross-workflow); removeDependency() drops the edge.
    dependencies: (id: string) => request<TaskDependencies>(`/tasks/${id}/dependencies`),
    addDependency: (id: string, dependsOnTaskId: string) =>
      request<void>(`/tasks/${id}/dependencies`, {
        method: 'POST',
        body: JSON.stringify({ depends_on_task_id: dependsOnTaskId }),
      }),
    removeDependency: (id: string, depId: string) =>
      request<void>(`/tasks/${id}/dependencies/${depId}`, { method: 'DELETE' }),
    // Subtasks (Mechanism 2). subtasks() lists a parent's children; createSubtask
    // adds one under the parent (lands on a human-gate label).
    subtasks: (id: string) => request<Task[]>(`/tasks?parent_id=${encodeURIComponent(id)}`),
    createSubtask: (id: string, body: { title: string; description?: string; type?: string; label?: string }) =>
      request<Task>(`/tasks/${id}/subtasks`, { method: 'POST', body: JSON.stringify(body) }),
    reviewComments: (id: string) => request<ReviewComment[]>(`/tasks/${id}/review-comments`),
    addReviewComment: (id: string, body: { file_path: string; side: 'old' | 'new'; start_line: number; end_line: number; quoted_text?: string; body: string }) =>
      request<ReviewComment>(`/tasks/${id}/review-comments`, { method: 'POST', body: JSON.stringify(body) }),
    updateReviewComment: (id: string, commentId: string, body: { status: 'resolved' | 'open'; resolution_note?: string }) =>
      request<ReviewComment>(`/tasks/${id}/review-comments/${commentId}`, { method: 'PATCH', body: JSON.stringify(body) }),
    deleteReviewComment: (id: string, commentId: string) =>
      request<void>(`/tasks/${id}/review-comments/${commentId}`, { method: 'DELETE' }),
    runs: (id: string) => request<AgentRun[]>(`/tasks/${id}/runs`),
    getRun: (id: string, runId: string) => request<AgentRun>(`/tasks/${id}/runs/${runId}`),
    // listLabelHistory returns the task's label-transition audit trail
    // (oldest first), including the resolved actor for human transitions.
    listLabelHistory: (id: string) => request<TaskLabelHistoryEntry[]>(`/tasks/${id}/label-history`),
    // cancelRun signals an in-flight run to stop. The pool marks the run
    // "cancelled" and pauses the task asynchronously, then broadcasts
    // task.agent_done, so callers rely on the WS event rather than the response.
    cancelRun: (id: string, runId: string) =>
      request<{ status: string; run_id: string }>(`/tasks/${id}/runs/${runId}/cancel`, { method: 'POST' }),
    // replyRun answers a waiting_human run's request_human question with text.
    // The backend starts a new run that resumes the prior provider session
    // where supported (claude) or starts cold with the reply in the prompt;
    // the task stays on its label. 202 + the new run id on success.
    replyRun: (id: string, runId: string, message: string) =>
      request<{ run_id: string }>(`/tasks/${id}/runs/${runId}/reply`, { method: 'POST', body: JSON.stringify({ message }) }),
    // runLogs returns a page of a run's log entries in chronological order
    // (oldest first). Omit `before` for the newest page (the tail); pass a
    // previous page's prevCursor as `before` to load earlier entries. hasMore
    // and prevCursor come from the X-Has-More / X-Prev-Cursor headers.
    runLogs: async (
      id: string,
      runId: string,
      opts?: { before?: string; limit?: number },
    ): Promise<{ items: AgentLog[]; hasMore: boolean; prevCursor: string | null }> => {
      const params = new URLSearchParams()
      if (opts?.before) params.set('before', opts.before)
      if (opts?.limit) params.set('limit', String(opts.limit))
      const qs = params.toString()
      const { data, headers } = await requestWithHeaders<AgentLog[]>(
        `/tasks/${id}/runs/${runId}/logs${qs ? `?${qs}` : ''}`,
      )
      return {
        items: data ?? [],
        hasMore: headers.get('X-Has-More') === 'true',
        prevCursor: headers.get('X-Prev-Cursor') || null,
      }
    },
  },
  workflows: {
    list: () => request<Workflow[]>('/workflows'),
    get: (id: string) => request<Workflow>(`/workflows/${id}`),
    create: (body: { name: string; description?: string }) =>
      request<Workflow>('/workflows', { method: 'POST', body: JSON.stringify(body) }),
    update: (id: string, body: { name: string; description: string; labels: { name: string; color: string; sort_order: number; agent_ignore: boolean; is_terminal: boolean }[]; transitions: { from_label: string; to_label: string; trigger_type: string; agent_config_id?: string; path?: string | null }[] }) =>
      request<Workflow>(`/workflows/${id}`, { method: 'PUT', body: JSON.stringify(body) }),
    delete: (id: string) => request<void>(`/workflows/${id}`, { method: 'DELETE' }),
    exportYaml: (id: string) => `${BASE}/workflows/${id}/export.yaml`,
    updateYaml: (id: string, yaml: string) =>
      request<Workflow>(`/workflows/${id}/yaml`, { method: 'PUT', body: yaml, headers: { 'Content-Type': 'application/yaml' } }),
    importYaml: (yaml: string) =>
      request<Workflow>('/workflows/import', { method: 'POST', body: yaml, headers: { 'Content-Type': 'application/yaml' } }),
  },
  agents: {
    list: () => request<AgentConfig[]>('/agents'),
    get: (id: string) => request<AgentConfig>(`/agents/${id}`),
    create: async (body: Omit<AgentConfig, 'id' | 'created_at' | 'updated_at' | 'enabled' | 'provider_config'>): Promise<{ config: AgentConfig; labelConflict?: string }> => {
      const res = await authedRawFetch(`${BASE}/agents`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json', ...authHeaders() },
        body: JSON.stringify(body),
      })
      if (!res.ok) {
        const err = await res.json().catch(() => ({ error: res.statusText }))
        throw new Error(err.error ?? res.statusText)
      }
      const config: AgentConfig = await res.json()
      const labelConflict = res.headers.get('X-Label-Conflict') ?? undefined
      return { config, labelConflict }
    },
    update: async (id: string, body: Omit<AgentConfig, 'id' | 'created_at' | 'updated_at' | 'provider_config'> & { enabled?: boolean }): Promise<{ config: AgentConfig; labelConflict?: string }> => {
      const res = await authedRawFetch(`${BASE}/agents/${id}`, {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(body),
      })
      if (!res.ok) {
        const err = await res.json().catch(() => ({ error: res.statusText }))
        throw new Error(err.error ?? res.statusText)
      }
      const config: AgentConfig = await res.json()
      const labelConflict = res.headers.get('X-Label-Conflict') ?? undefined
      return { config, labelConflict }
    },
    delete: (id: string) => request<void>(`/agents/${id}`, { method: 'DELETE' }),
    models: (provider: string) => request<ModelList>(`/agents/models?provider=${provider}`),
    claudeOptions: () => request<ClaudeOptions>('/agents/claude-options'),
  },
  providerConfigs: {
    list: () => request<ProviderConfig[]>('/provider-configs'),
    get: (id: string) => request<ProviderConfig>(`/provider-configs/${id}`),
    create: (body: Omit<ProviderConfig, 'id' | 'created_at' | 'updated_at'>) =>
      request<ProviderConfig>('/provider-configs', { method: 'POST', body: JSON.stringify(body) }),
    update: (id: string, body: Omit<ProviderConfig, 'id' | 'created_at' | 'updated_at'>) =>
      request<ProviderConfig>(`/provider-configs/${id}`, { method: 'PUT', body: JSON.stringify(body) }),
    delete: (id: string) => request<void>(`/provider-configs/${id}`, { method: 'DELETE' }),
  },
  repos: {
    list: () => request<Repo[]>('/repos'),
    get: (id: string) => request<Repo>(`/repos/${id}`),
    create: (body: { name?: string; path?: string; remote_url?: string; workflow_id?: string; issue_sync_enabled?: boolean; issue_sync_label?: string; issue_writeback_enabled?: boolean; pr_review_auto_transition_enabled?: boolean }) =>
      request<Repo>('/repos', { method: 'POST', body: JSON.stringify(body) }),
    update: (id: string, body: { name?: string; path?: string; remote_url?: string | null; workflow_id?: string | null; issue_sync_enabled?: boolean; issue_sync_label?: string; issue_writeback_enabled?: boolean; pr_review_auto_transition_enabled?: boolean }) =>
      request<Repo>(`/repos/${id}`, { method: 'PATCH', body: JSON.stringify(body) }),
    delete: (id: string) => request<void>(`/repos/${id}`, { method: 'DELETE' }),
    tree: (id: string, ref = 'HEAD') => request<{ ref: string; files: string[] }>(`/repos/${id}/tree?ref=${ref}`),
  },
  templates: {
    list: () => request<TaskTemplate[]>('/templates'),
    create: (body: { name: string; title?: string; description?: string; type?: string }) =>
      request<TaskTemplate>('/templates', { method: 'POST', body: JSON.stringify(body) }),
    update: (id: string, body: { name: string; title?: string; description?: string; type?: string }) =>
      request<TaskTemplate>(`/templates/${id}`, { method: 'PUT', body: JSON.stringify(body) }),
    delete: (id: string) => request<void>(`/templates/${id}`, { method: 'DELETE' }),
  },
  schedules: {
    list: () => request<TaskSchedule[]>('/schedules'),
    get: (id: string) => request<TaskSchedule>(`/schedules/${id}`),
    create: (body: { template_id: string; repo_id: string; cron_expr: string; target_label?: string; enabled?: boolean }) =>
      request<TaskSchedule>('/schedules', { method: 'POST', body: JSON.stringify(body) }),
    update: (id: string, body: { cron_expr: string; target_label?: string; enabled?: boolean }) =>
      request<TaskSchedule>(`/schedules/${id}`, { method: 'PUT', body: JSON.stringify(body) }),
    delete: (id: string) => request<void>(`/schedules/${id}`, { method: 'DELETE' }),
  },
  dashboard: {
    get: () => request<Dashboard>('/dashboard'),
    // Full per-task cost rollup (no top-N cap), used by the board page to
    // compute the cost of the currently-selected filter.
    costByTask: () => request<TaskCost[]>('/dashboard/cost-by-task'),
  },
  github: {
    authStatus: () => request<{ authed: boolean; note: string }>('/github/auth-status'),
  },
  health: {
    providers: () => request<{ checks: ProviderCheck[] }>('/health/providers'),
  },
  chat: {
    list: () => request<ChatSession[]>('/chat/sessions'),
    create: (body: { repo_id: string; provider_config_id: string; title?: string }) =>
      request<ChatSession>('/chat/sessions', { method: 'POST', body: JSON.stringify(body) }),
    get: (id: string) => request<{ session: ChatSession; provider_config?: ProviderConfig }>(`/chat/sessions/${id}`),
    delete: (id: string) => request<void>(`/chat/sessions/${id}`, { method: 'DELETE' }),
  },
  backup: {
    // Raw binary download — not a JSON request<T>() call, mirrors
    // workflows.exportYaml. Callers must fetch() this URL themselves via
    // authedRawFetch (browsers can't set headers on <a href>, and downloads
    // need the same Authorization header as everything else).
    url: () => `${BASE}/backup`,
    getSettings: () => request<BackupSettings>('/backup/settings'),
    updateSettings: (body: { interval_seconds: number; keep: number }) =>
      request<BackupSettings>('/backup/settings', { method: 'PUT', body: JSON.stringify(body) }),
  },
  logRetention: {
    getSettings: () => request<LogRetentionSettings>('/log-retention/settings'),
    updateSettings: (body: { days: number; interval_seconds: number }) =>
      request<LogRetentionSettings>('/log-retention/settings', { method: 'PUT', body: JSON.stringify(body) }),
  },
  modelPricing: {
    list: () => request<ModelPricing[]>('/settings/pricing'),
    // Replaces the entire table — add/remove/edit a model are all expressed
    // client-side as a new full list (see PricingSettingsPage).
    update: (rows: { model: string; input_per_1m: number; output_per_1m: number }[]) =>
      request<ModelPricing[]>('/settings/pricing', { method: 'PUT', body: JSON.stringify(rows) }),
  },
  uploads: {
    // Raw binary download — mirrors backup.url(). Callers must fetch() this
    // URL themselves via authedRawFetch since <img>/window.open can't carry
    // an Authorization header, and BASE_URL must be respected for prod
    // deployments served under a sub-path (e.g. nginx `/tasks/`).
    downloadUrl: (rel: string) => `${BASE}/uploads/${rel}`,
  },
}
