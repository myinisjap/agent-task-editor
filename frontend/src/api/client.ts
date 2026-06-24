const BASE = '/api/v1'

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(`${BASE}${path}`, {
    headers: { 'Content-Type': 'application/json', ...init?.headers },
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
  created_at: string
  updated_at: string
}

export type AgentRun = {
  id: string
  task_id: string
  agent_config_id: string
  status: string
  feedback?: string
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
    create: (body: { title: string; description?: string; type?: string; repo_id: string; workflow_id: string }) =>
      request<Task>('/tasks', { method: 'POST', body: JSON.stringify(body) }),
    update: (id: string, body: { title?: string; description?: string; type?: string }) =>
      request<Task>(`/tasks/${id}`, { method: 'PATCH', body: JSON.stringify(body) }),
    delete: (id: string) => request<void>(`/tasks/${id}`, { method: 'DELETE' }),
    moveLabel: (id: string, to_label: string, note?: string) =>
      request<Task>(`/tasks/${id}/label`, { method: 'PATCH', body: JSON.stringify({ to_label, note }) }),
    approve: (id: string, note?: string) =>
      request<Task>(`/tasks/${id}/approve`, { method: 'POST', body: JSON.stringify({ note }) }),
    reject: (id: string, note: string, to_label?: string) =>
      request<Task>(`/tasks/${id}/reject`, { method: 'POST', body: JSON.stringify({ note, to_label }) }),
    runs: (id: string) => request<AgentRun[]>(`/tasks/${id}/runs`),
    runLogs: (id: string, runId: string) => request<AgentLog[]>(`/tasks/${id}/runs/${runId}/logs`),
  },
  workflows: {
    list: () => request<Workflow[]>('/workflows'),
    get: (id: string) => request<Workflow>(`/workflows/${id}`),
    update: (id: string, body: { name: string; description: string; labels: Omit<WorkflowLabel, 'id' | 'workflow_id'>[]; transitions: { from_label: string; to_label: string; trigger_type: string; agent_config_id?: string }[] }) =>
      request<Workflow>(`/workflows/${id}`, { method: 'PUT', body: JSON.stringify(body) }),
  },
  agents: {
    list: () => request<AgentConfig[]>('/agents'),
    get: (id: string) => request<AgentConfig>(`/agents/${id}`),
    create: (body: Omit<AgentConfig, 'id' | 'created_at' | 'updated_at'>) =>
      request<AgentConfig>('/agents', { method: 'POST', body: JSON.stringify(body) }),
    update: (id: string, body: Omit<AgentConfig, 'id' | 'created_at' | 'updated_at'>) =>
      request<AgentConfig>(`/agents/${id}`, { method: 'PUT', body: JSON.stringify(body) }),
    delete: (id: string) => request<void>(`/agents/${id}`, { method: 'DELETE' }),
  },
  repos: {
    list: () => request<Repo[]>('/repos'),
    get: (id: string) => request<Repo>(`/repos/${id}`),
    create: (body: { name: string; path: string; remote_url?: string; workflow_id?: string }) =>
      request<Repo>('/repos', { method: 'POST', body: JSON.stringify(body) }),
    delete: (id: string) => request<void>(`/repos/${id}`, { method: 'DELETE' }),
    tree: (id: string, ref = 'HEAD') => request<{ ref: string; files: string[] }>(`/repos/${id}/tree?ref=${ref}`),
    diff: (id: string, base = 'HEAD~1', head = 'HEAD') =>
      request<{ base: string; head: string; diff: string }>(`/repos/${id}/diff?base=${base}&head=${head}`),
  },
  dashboard: {
    get: () => request<Dashboard>('/dashboard'),
  },
}
