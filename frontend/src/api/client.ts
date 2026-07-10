import { authHeaders, notifyUnauthorized } from './authToken'

const BASE = `${import.meta.env.BASE_URL.replace(/\/$/, '')}/api/v1`

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
  const headers: Record<string, string> = {}
  if (!init?.isFormData) {
    headers['Content-Type'] = 'application/json'
  }
  const res = await authedRawFetch(`${BASE}${path}`, {
    headers: { ...headers, ...init?.headers },
    ...init,
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
    headers: { 'Content-Type': 'application/json', ...init?.headers },
    ...init,
  })
  if (!res.ok) {
    const err = await res.json().catch(() => ({ error: res.statusText }))
    throw new Error(err.error ?? res.statusText)
  }
  const data = (res.status === 204 ? undefined : await res.json()) as T
  return { data, headers: res.headers }
}

// Cursor-paginated result: a page of items plus the cursor for the next page
// (null when the list is exhausted).
export type Page<T> = { items: T[]; nextCursor: string | null }

// ----- Tasks -----

export type Task = {
  id: string
  title: string
  description: string
  type: string
  label: string
  repo_id: string
  workflow_id: string
  current_agent_run_id?: string
  agent_notes?: string
  attachments?: string[]
  created_at: string
  updated_at: string
  // git / PR tracking fields
  branch?: string
  worktree_path?: string
  base_ref?: string
  git_state?: string
  // URL of the GitHub PR opened for this task's branch (via POST /tasks/{id}/pr
  // or discovered by the ghsync sweep). Empty until a PR exists.
  pr_url?: string
  paused?: boolean
  // Archived tasks are hidden from the board by default (GET /tasks excludes
  // them unless archived=all|only), skipped by the ghsync sweep, and never
  // dispatched to agents. Independent of label.
  archived?: boolean
  // Transient-error auto-retry state (see AgentConfig.max_retries /
  // retry_backoff_secs). next_retry_at is set while the task is in a
  // backed-off auto-retry window and cleared on success, genuine failure, or
  // once the retry budget is exhausted.
  transient_retry_count?: number
  next_retry_at?: string | null
  // Where this task was imported from ("github") and the external item it
  // came from ("owner/repo#123"). Both empty for manually created tasks.
  source?: string
  source_ref?: string
  // Derived (read-time) dependency counts. blocked_by_count is the number of
  // this task's blockers whose edges are still unsatisfied — while > 0 the task
  // is never dispatched. blocking_count is how many tasks depend on this one.
  blocked_by_count?: number
  blocking_count?: number
  // Subtask decomposition (Mechanism 2). parent_task_id is set on a child;
  // created_by_run_id records the agent run that created it; merge_status tracks
  // a child's branch merge-back into the parent ('' | pending | merged |
  // merge_conflict). subtask_* are derived rollups on a parent.
  parent_task_id?: string | null
  created_by_run_id?: string | null
  merge_status?: string
  subtask_total?: number
  subtask_done?: number
  subtask_conflicts?: number
  // Advisory per-task cost budget cap in USD (see AgentConfig.max_cost_usd
  // for the full semantics — the effective budget the dispatcher enforces
  // is the lower of this and the matched agent config's cap). 0/undefined
  // means unlimited from the task side.
  max_cost_usd?: number
  // Dispatch priority: -1=low, 0=normal (default), 1=high, 2=urgent.
  // ListAgentPickupTasks orders eligible tasks by priority DESC, then
  // created_at ASC, so higher-priority tasks are dispatched first when there
  // are more eligible tasks than free workers (MAX_WORKERS).
  priority?: number
  // Derived, read-time 0-based position in the current agent-pickup queue
  // (priority DESC, created_at ASC) among tasks eligible for dispatch.
  // Null/absent when the task is not currently pickup-eligible (e.g.
  // blocked, paused, archived, or not on an agent-triggerable label).
  queue_position?: number | null
}

// DependencyEdge is one end of a task dependency edge (a blocker or a
// dependent). `satisfied` is only meaningful for blockers.
export type DependencyEdge = {
  task_id: string
  title: string
  label: string
  archived: boolean
  satisfied: boolean
}

// TaskDependencies is both directions of a task's dependency edges.
export type TaskDependencies = {
  blocked_by: DependencyEdge[]
  blocking: DependencyEdge[]
  blocked_by_count: number
  blocking_count: number
}

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

// TaskTemplate pre-fills title/description/type in the new-task form for
// recurring shapes of work ("upgrade dependency X", "fix flaky test").
export type TaskTemplate = {
  id: string
  name: string
  title: string
  description: string
  type: string
  created_at: string
  updated_at: string
}

export type AgentRun = {
  id: string
  task_id: string
  agent_config_id: string
  status: string
  feedback?: string
  stored_info?: string
  notes?: string | null
  started_at?: string
  completed_at?: string
  created_at: string
  // Token usage / estimated cost for this run. 0 if the provider does not
  // report usage (e.g. opencode currently). For the `claude`/`qwen_code`
  // CLI providers, cost_usd is the CLI's own authoritative total_cost_usd
  // figure (which may legitimately be 0 under a Claude Max subscription);
  // for anthropic/llm providers it's computed from tokens via an internal
  // pricing table. For `gemini_cli`/`codex_cli`, only input/output token
  // counts are reported by the CLI's JSON output (no cost figure) — cost_usd
  // is left at 0, not estimated.
  input_tokens?: number
  output_tokens?: number
  cost_usd?: number
  // Provider-side conversation session for this run (claude/qwen stream-json
  // session_id, or the gemini_cli/codex_cli session/thread id). Only the
  // `claude` provider currently resumes a prior session on a later run.
  session_id?: string
}

// TaskLabelHistoryEntry is one row of a task's label-transition audit trail
// (task_label_history), oldest first. For human-triggered transitions,
// actor_id is the resolved named-token actor (see backend BearerAuth /
// ActorFromContext) — null/empty when the legacy shared token or no auth was
// used. For agent-triggered transitions, actor_id is the agent run ID.
export type TaskLabelHistoryEntry = {
  id: string
  task_id: string
  from_label: string | null
  to_label: string
  trigger: string
  actor_id: string | null
  note: string | null
  created_at: string
}

// ReviewComment is a persistent, file/line-anchored inline comment on a
// task's diff. Open comments are injected into every agent run's prompt until
// resolved (by the agent via the MCP resolve_comment tool, or by a human).
export type ReviewComment = {
  id: string
  task_id: string
  file_path: string
  side: 'old' | 'new'
  start_line: number
  end_line: number
  quoted_text: string
  body: string
  status: 'open' | 'resolved'
  resolution_note?: string | null
  resolved_by_run_id?: string | null
  created_at: string
  updated_at: string
}

export type AgentLog = {
  id: string
  agent_run_id: string
  timestamp: string
  type: string
  content: string
}

export type WorkflowLabel = {
  id: string
  workflow_id: string
  name: string
  color: string
  sort_order: number
  agent_ignore: number
  is_terminal: number
}

export type WorkflowTransition = {
  id: string
  workflow_id: string
  from_label: string
  to_label: string
  trigger_type: 'agent' | 'human' | 'both'
  agent_config_id?: string
  path?: 'success' | 'failure' | 'either' | null
}

export type Workflow = {
  id: string
  name: string
  description: string
  labels: WorkflowLabel[]
  transitions: WorkflowTransition[]
  created_at: string
  updated_at: string
}

export type AgentConfig = {
  id: string
  name: string
  provider: string
  model: string
  system_prompt: string
  labels: string
  env: string
  max_tokens: number
  timeout_secs: number
  max_turns: number
  // Retry policy for transient provider errors (rate limits, network blips,
  // upstream 5xx) — distinct from genuine task failures. max_retries=0
  // disables auto-retry (today's unbounded-immediate-redispatch behavior).
  max_retries: number
  retry_backoff_secs: number
  enabled: number | boolean
  // JSON-string-encoded arrays, consistent with how `labels`/`env` are stored.
  // enabled_plugins: Claude plugin IDs ("<name>@<marketplace>"), claude-provider only.
  // enabled_mcp_servers: Claude MCP server names (from ~/.claude.json), claude-provider only.
  enabled_plugins?: string
  enabled_mcp_servers?: string
  // command_allowlist / command_denylist: JSON array of shell-command glob patterns
  // ("*" wildcard). Both default to "[]" (no restriction). Best-effort string
  // matching, not a sandbox. Denylist is always checked first.
  // Allowlist: fully enforced for anthropic, llm, qwen_code. NOT an effective
  // restriction for claude (CLI only auto-approves matches; see docs). Not enforced
  // for opencode, gemini_cli, or codex_cli (codex_cli has its own native
  // sandbox/approval-mode system instead — see docs/providers/codex_cli.md).
  // Denylist: fully enforced for anthropic, llm, claude. NOT enforced for qwen_code
  // (no confirmed CLI flag), opencode, gemini_cli, or codex_cli.
  command_allowlist?: string
  command_denylist?: string
  // Whether new runs resume the previous run's provider session (claude
  // provider only; on by default). Off = every run starts cold ("fresh eyes").
  resume_sessions?: boolean
  // Whether this config's runs can decompose their task into subtasks via the
  // create_subtask MCP tool (claude/qwen_code/gemini_cli/codex_cli only; off
  // by default). max_subtasks caps children per parent.
  subtasks_enabled?: boolean
  max_subtasks?: number
  // Advisory per-task cost budget cap in USD, checked by the dispatcher
  // before each sweep-dispatch against the task's cumulative recorded run
  // cost so far (across every run for the task, not just terminal ones).
  // 0 disables the cap (unlimited). If the task itself also has a nonzero
  // max_cost_usd, the effective budget is the lower of the two. This is
  // NOT a mid-run kill switch — no provider supports killing an in-flight
  // run at a cost threshold, so a single expensive run can still exceed
  // the budget; the guard only blocks the *next* dispatch.
  max_cost_usd: number
  created_at: string
  updated_at: string
}

export type Repo = {
  id: string
  name: string
  path: string
  remote_url?: string
  workflow_id?: string
  created_at: string
  // GitHub Issues import: when enabled (1), open issues matching
  // issue_sync_label (empty = all open issues) are imported as tasks.
  issue_sync_enabled?: number
  issue_sync_label?: string
}

export type ModelList = {
  provider: string
  default_model: string
  models: string[]
}

export type ClaudeOptions = {
  plugins: { id: string; name: string; marketplace: string }[]
  mcp_servers: string[]
}

export type Dashboard = {
  label_counts: Record<string, number>
  active_agents: { run_id: string; task_id: string; task_title: string; agent_name: string; started_at: string }[]
  intervention_queue: { run_id: string; task_id: string; task_title: string; message?: string; created_at: string }[]
  // Aggregate token/cost usage across all runs in a terminal state
  // (completed, failed, waiting_human).
  cost_total?: { input_tokens: number; output_tokens: number; cost_usd: number }
  // Per-provider breakdown, sorted by cost descending. Runs whose
  // agent_config was later deleted are excluded (agent_config_id is set
  // NULL on delete, so they can no longer be attributed to a provider).
  cost_by_provider?: { provider: string; input_tokens: number; output_tokens: number; cost_usd: number; run_count: number }[]
  // Per-agent-config run analytics, sorted by run_count descending. Same
  // terminal-state + still-existing-agent_config filtering as
  // cost_by_provider. Two caveats: (1) avg_turns_to_done and the retry
  // fields are attributed to a task's *last* run's agent config only, not
  // split across every config a task passed through; (2)
  // avg_transient_retries/tasks_with_retries are a live snapshot of
  // tasks.transient_retry_count, which resets on success/escalation — not a
  // lifetime/historical retry count.
  agent_config_stats?: {
    agent_config_id: string
    agent_name: string
    provider: string
    run_count: number
    completed_count: number
    failed_count: number
    waiting_human_count: number
    success_rate_percent: number
    avg_duration_secs: number
    p90_duration_secs: number
    avg_turns_to_done: number
    avg_transient_retries: number
    tasks_with_retries: number
    input_tokens: number
    output_tokens: number
    cost_usd: number
  }[]
  // Daily token/cost/run-count rollup, most recent day first, last 30 days
  // with recorded activity. Same terminal-state filtering as cost_total.
  cost_by_day?: { day: string; input_tokens: number; output_tokens: number; cost_usd: number; run_count: number }[]
  // Top 20 tasks by cumulative recorded cost, across ALL runs regardless of
  // status (unlike cost_total/cost_by_provider/agent_config_stats, which
  // only count terminal-state runs) — a cost rollup should reflect every
  // run that ran, including ones still in flight or that failed.
  cost_by_task?: { task_id: string; task_title: string; input_tokens: number; output_tokens: number; cost_usd: number }[]
  // Live Claude account rate-limit usage from Anthropic's OAuth usage
  // endpoint (5-hour rolling window + weekly window). `available` is false
  // when the server has no Claude OAuth credentials or the fetch failed;
  // other fields are zero/absent in that case.
  claude_usage?: {
    available: boolean
    five_hour_percent?: number
    five_hour_resets_at?: string | null
    weekly_percent?: number
    weekly_resets_at?: string | null
  }
}

// TaskCost is a single row of the { task_id, cost_usd } cost rollup returned
// by GET /dashboard/cost-by-task, used by the board page to compute the
// total cost of the currently-selected filter. Unlike Dashboard.cost_by_task
// this endpoint returns every task (no top-N cap, no title) since the board
// needs a cost for every visible task, not just the most expensive ones.
export type TaskCost = { task_id: string; input_tokens: number; output_tokens: number; cost_usd: number }

export type ProviderCheckStatus = 'ok' | 'warn' | 'error'

export type ProviderCheck = {
  id: string
  name: string
  status: ProviderCheckStatus
  detail: string
  hint?: string
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
      request<{ pr_url: string; git_state: string }>(`/tasks/${id}/pr`, { method: 'POST' }),
    githubStatus: (id: string) =>
      request<{ git_state: string; pr_url: string; error?: string }>(`/tasks/${id}/github-status`),
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
    create: async (body: Omit<AgentConfig, 'id' | 'created_at' | 'updated_at' | 'enabled'>): Promise<{ config: AgentConfig; labelConflict?: string }> => {
      const res = await authedRawFetch(`${BASE}/agents`, {
        method: 'POST',
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
    update: (id: string, body: Omit<AgentConfig, 'id' | 'created_at' | 'updated_at'> & { enabled?: boolean }) =>
      request<AgentConfig>(`/agents/${id}`, { method: 'PUT', body: JSON.stringify(body) }),
    delete: (id: string) => request<void>(`/agents/${id}`, { method: 'DELETE' }),
    models: (provider: string) => request<ModelList>(`/agents/models?provider=${provider}`),
    claudeOptions: () => request<ClaudeOptions>('/agents/claude-options'),
  },
  repos: {
    list: () => request<Repo[]>('/repos'),
    get: (id: string) => request<Repo>(`/repos/${id}`),
    create: (body: { name?: string; path?: string; remote_url?: string; workflow_id?: string; issue_sync_enabled?: boolean; issue_sync_label?: string }) =>
      request<Repo>('/repos', { method: 'POST', body: JSON.stringify(body) }),
    update: (id: string, body: { name?: string; path?: string; remote_url?: string | null; workflow_id?: string | null; issue_sync_enabled?: boolean; issue_sync_label?: string }) =>
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
  backup: {
    // Raw binary download — not a JSON request<T>() call, mirrors
    // workflows.exportYaml. Callers must fetch() this URL themselves via
    // authedRawFetch (browsers can't set headers on <a href>, and downloads
    // need the same Authorization header as everything else).
    url: () => `${BASE}/backup`,
  },
}
