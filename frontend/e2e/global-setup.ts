// Registers a demo repo against the running backend before the e2e suite
// starts, so board.spec.ts can create a task without a manual seeding step.
//
// The repo directory itself must already exist on disk at the path that
// becomes REPO_BASE_DIR (bind-mounted into the backend container) — that's
// a prerequisite of the *stack*, not something this Node process can do on
// the backend's behalf, since the backend runs in a separate container.
// CI creates that directory on the runner (which is also the docker host)
// before `docker compose up`; see .github/workflows/ci.yml.
//
// This only calls the backend's REST API directly (not through the
// frontend's nginx proxy), mirroring scripts/seed-demo.sh.

const API_BASE = process.env.E2E_API_BASE_URL ?? 'http://localhost:8080/api/v1'
const REPO_PATH = process.env.E2E_DEMO_REPO_PATH ?? '/tmp/repos/demo-repo'
const REPO_NAME = 'e2e-demo-repo'

async function json<T>(res: Response): Promise<T> {
  if (!res.ok) {
    throw new Error(`${res.status} ${res.statusText}: ${await res.text()}`)
  }
  return res.json() as Promise<T>
}

export default async function globalSetup() {
  // Idempotent: if a repo with this name already exists (e.g. re-running
  // locally against a stack that wasn't torn down), reuse it instead of
  // erroring on a duplicate-name conflict.
  const existing = await json<Array<{ id: string; name: string; workflow_id?: string }>>(
    await fetch(`${API_BASE}/repos`),
  )
  let repo = existing.find((r) => r.name === REPO_NAME)

  if (!repo) {
    repo = await json(
      await fetch(`${API_BASE}/repos`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ name: REPO_NAME, path: REPO_PATH }),
      }),
    )
  }

  const workflows = await json<Array<{ id: string }>>(await fetch(`${API_BASE}/workflows`))
  const workflowId = workflows[0]?.id
  if (!workflowId) {
    throw new Error('No workflow found — backend should auto-seed a default workflow on first boot')
  }

  if (repo!.workflow_id !== workflowId) {
    await fetch(`${API_BASE}/repos/${repo!.id}`, {
      method: 'PATCH',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ workflow_id: workflowId }),
    })
  }

  // Exposed to tests via process.env (Playwright config/tests run in the
  // same Node process as globalSetup).
  process.env.E2E_REPO_NAME = REPO_NAME
  process.env.E2E_WORKFLOW_ID = workflowId
}
