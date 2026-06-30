const BASE = '/api/v1'

async function request<T>(path: string, init?: RequestInit & { isFormData?: boolean }): Promise<T> {
  const headers: Record<string, string> = {}
  if (!init?.isFormData) {
    headers['Content-Type'] = 'application/json'
  }
  const res = await fetch(`${BASE}${path}`, {
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
  paused?: boolean
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
  enabled: number | boolean
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
}

export type ModelList = {
  provider: string
  default_model: string
  models: string[]
}

export type Dashboard = {
  label_counts: Record<string, number>
  active_agents: { run_id: string; task_id: string; task_title: string; agent_name: string; started_at: string }[]
  intervention_queue: { run_id: string; task_id: string; task_title: string; message?: string; created_at: string }[]
}

export const api = {
  tasks: {
    list: (label?: string) =>
      request<Task[]>(`/tasks${label ? `?label=${label}` : ''}`),
    get: (id: string) => request<Task>(`/tasks/${id}`),
    create: (body: FormData | { title: string; description?: string; type?: string; repo_id: string; workflow_id: string }) => {
      if (body instanceof FormData) {
        return request<Task>('/tasks', { method: 'POST', body, isFormData: true })
      }
      return request<Task>('/tasks', { method: 'POST', body: JSON.stringify(body) })
    },
    update: (id: string, body: { title?: string; description?: string; type?: string; repo_id?: string }) =>
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
    githubStatus: (id: string) =>
      request<{ git_state: string; pr_url: string; error?: string }>(`/tasks/${id}/github-status`),
    updateGitState: (id: string, git_state: string) =>
      request<Task>(`/tasks/${id}/git-state`, { method: 'PATCH', body: JSON.stringify({ git_state }) }),
    setPaused: (id: string, paused: boolean) =>
      request<Task>(`/tasks/${id}/pause`, { method: 'PATCH', body: JSON.stringify({ paused }) }),
    runs: (id: string) => request<AgentRun[]>(`/tasks/${id}/runs`),
    getRun: (id: string, runId: string) => request<AgentRun>(`/tasks/${id}/runs/${runId}`),
    runLogs: (id: string, runId: string) => request<AgentLog[]>(`/tasks/${id}/runs/${runId}/logs`),
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
      const res = await fetch(`${BASE}/agents`, {
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
  },
  repos: {
    list: () => request<Repo[]>('/repos'),
    get: (id: string) => request<Repo>(`/repos/${id}`),
    create: (body: { name?: string; path?: string; remote_url?: string; workflow_id?: string }) =>
      request<Repo>('/repos', { method: 'POST', body: JSON.stringify(body) }),
    update: (id: string, body: { name?: string; path?: string; remote_url?: string | null; workflow_id?: string | null }) =>
      request<Repo>(`/repos/${id}`, { method: 'PATCH', body: JSON.stringify(body) }),
    delete: (id: string) => request<void>(`/repos/${id}`, { method: 'DELETE' }),
    tree: (id: string, ref = 'HEAD') => request<{ ref: string; files: string[] }>(`/repos/${id}/tree?ref=${ref}`),
  },
  dashboard: {
    get: () => request<Dashboard>('/dashboard'),
  },
  github: {
    authStatus: () => request<{ authed: boolean; note: string }>('/github/auth-status'),
  },
}
