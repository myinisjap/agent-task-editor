   
   
   # Agent Task Notes — Technical Spec & Implementation Plan
   
   ## Problem Statement

   Agents (Claude CLI subprocesses) need to write structured notes back to a task so that subsequent agents can read those notes. Two key
   flows:
   1. A **plan agent** writes its plan to the task → a **code agent** reads the plan and implements it
   2. A **review agent** writes identified issues/feedback to the task → a **code agent** reads the feedback and fixes it
   
   ## Current State Analysis
   
   ### Existing Inter-Agent Data Flow
   
   1. **Feedback path (working):** When a human rejects a task, `agent_runs.feedback` is set. The dispatcher reads it at
   `dispatcher.go:104-106` via `prior.Feedback` and passes it to `RunInput.Feedback`. In `buildPrompt` at `claude.go:219-223`, it appears
   as `FEEDBACK FROM PRIOR REVIEW:`.
2. **PriorPlan path (dead code):** `RunInput.PriorPlan` at `provider.go:47` is consumed by `buildPrompt` at `claude.go:224-227` as
   `IMPLEMENTATION PLAN:`, but the dispatcher never sets it. No database column exists for plan storage.

   3. **MCP sidecar:** The sidecar at `cmd/mcp-server/main.go` communicates results via a temp file (`RESULT_FILE`). It currently
   supports `signal_complete` and `request_human`. The result struct has `Status`, `NextLabel`, and `Message` fields.

   ### Key Architectural Constraints

   - The MCP sidecar is a standalone binary with no database access — it communicates via `RESULT_FILE` (a JSON file read after process
   exit).
   - The `agent_runs.feedback` column stores per-run rejection notes, not general-purpose notes.
   - sqlc codegen is configured at `sqlc.yaml` with schema pointing to `001_init.up.sql` only. New columns need the schema file updated
   for codegen.

   ---

   ## Technical Specification

   ### Data Model

   Add an `agent_notes` TEXT column to the `tasks` table (default empty string).

   **Migration `004_task_agent_notes.up.sql`:**
   ```sql
   ALTER TABLE tasks ADD COLUMN agent_notes TEXT NOT NULL DEFAULT '';
   ```
**Migration `004_task_agent_notes.down.sql`:**
   ```sql
   -- SQLite does not support DROP COLUMN before 3.35.0;
   -- for safety, recreate the table without the column
   ```

   **sqlc schema update:** Add the column to the `tasks` CREATE TABLE in `001_init.up.sql` so sqlc generates the correct model (sqlc
   reads this file for codegen; the migration handles actual DB changes at runtime).

   ### MCP Tool: `update_task_notes`

   New tool in `cmd/mcp-server/main.go`:

   ```
   Tool name: update_task_notes
   Description: "Write structured notes to the task for subsequent agents to read.
                Use this to record plans, analysis, review findings, or any context
                that the next agent in the workflow should have."
   Input schema:
     - notes (string, required): "The notes content (supports markdown)"
     - append (boolean, optional, default false): "If true, append to existing notes
       instead of replacing them"
   ```

   **Communication mechanism — Extend RESULT_FILE protocol:** Add a `Notes` field to the `result` struct. The sidecar accumulates notes
   in memory across multiple `update_task_notes` calls and writes the final value to `RESULT_FILE` alongside the status. The pool reads
   this after the agent exits and persists notes to the DB.

This fits the existing architecture perfectly — no new communication channels needed.

   ### Result Struct Changes

   In both `cmd/mcp-server/main.go` and `agent/provider.go`:
   ```go
   type Result struct {
       Status    string
       NextLabel *string
       Message   *string
       Notes     *string  // NEW: agent-written notes to persist on the task
   }
   ```

   ### API Endpoint

   **`PATCH /api/v1/tasks/{id}/notes`**

   Request body:
   ```json
   {
     "notes": "## Plan\n1. Refactor auth module\n2. Add tests",
     "append": false
   }
   ```

   Response: Updated `Task` object (which now includes `agent_notes`).
### Prompt Injection

   In `dispatcher.go`, when building `RunInput`, read `task.AgentNotes` and pass it as `RunInput.PriorPlan` (reuse existing field). The
   `buildPrompt` function at `claude.go:224-227` already handles injection. Update the header text from `IMPLEMENTATION PLAN:` to `NOTES
   FROM PRIOR AGENT:` for generality.

   ### Frontend Display

   In `TaskDetailPage.tsx`, add an "Agent Notes" section in the left metadata panel, below the task description. Render as a preformatted
   block for v1. Show only when `task.agent_notes` is non-empty.

   ---

   ## Design Decisions

   | Decision | Chosen | Alternative | Rationale |
   |----------|--------|-------------|-----------|
   | Storage | Single `agent_notes` column on `tasks` | Separate `task_notes` table with per-run history | Simpler schema, 1:1 with task,
   no joins needed. History can be added later via `task_label_history.note` which already exists. |
   | Sidecar communication | Extend RESULT_FILE protocol | HTTP callback to main server | Zero new communication channels, fits existing
   arch. Notes only matter when next agent starts, not mid-run. |
   | Prompt injection | Reuse `RunInput.PriorPlan` field | Add new `AgentNotes` field | Field already exists and is consumed by
   `buildPrompt`. Just wire it up. |

   ---

   ## Implementation Plan (Ordered Steps)

   ### Step 1: Database Migration + sqlc

**Files to create:**
   - `backend/internal/storage/migrations/004_task_agent_notes.up.sql`
   - `backend/internal/storage/migrations/004_task_agent_notes.down.sql`

   **Files to modify:**
   - `backend/internal/storage/migrations/001_init.up.sql` — add `agent_notes TEXT NOT NULL DEFAULT ''` to the `tasks` CREATE TABLE
   (before `created_at`, around line 63)
   - `backend/internal/storage/queries/tasks.sql` — add `agent_notes` to every SELECT column list; add new query:
     ```sql
     -- name: UpdateTaskNotes :one
     UPDATE tasks SET agent_notes = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?
     RETURNING id, title, description, type, label, repo_id, workflow_id, current_agent_run_id, created_at, updated_at,
   active_agent_run_id, agent_notes;
     ```

   **Then run:** `cd backend && sqlc generate`

   ### Step 2: Result Struct Extension

   **Files to modify:**
   - `backend/internal/agent/provider.go` — add `Notes *string` to `Result` struct (after `Message`, line ~35)
   - `backend/cmd/mcp-server/main.go` — add `Notes *string` to the `result` struct (line ~15)

   ### Step 3: MCP Sidecar — `update_task_notes` Tool

   **File to modify:** `backend/cmd/mcp-server/main.go`
1. Add a package-level `var currentNotes string` accumulator
   2. Add `update_task_notes` to the `tools/list` response (3rd tool entry after `request_human`, around line 98):
      ```go
      {
          "name":        "update_task_notes",
          "description": "Write structured notes to the task for subsequent agents to read. Use this to record plans, analysis, review
   findings, or any context that the next agent in the workflow should have.",
          "inputSchema": map[string]any{
              "type": "object",
              "properties": map[string]any{
                  "notes":  map[string]any{"type": "string", "description": "The notes content (supports markdown)"},
                  "append": map[string]any{"type": "boolean", "description": "If true, append to existing notes instead of replacing"},
              },
              "required": []string{"notes"},
          },
      },
      ```
   3. Add case in `dispatchTool` for `"update_task_notes"`:
      ```go
      case "update_task_notes":
          var a struct {
              Notes  string `json:"notes"`
              Append bool   `json:"append"`
          }
          _ = json.Unmarshal(args, &a)
          if a.Append && currentNotes != "" {
              currentNotes = currentNotes + "\n\n" + a.Notes
          } else {
              currentNotes = a.Notes
          }
          return "notes updated", nil
      ```
4. Update `signal_complete` and `request_human` handlers to include `currentNotes` in the returned result:
      ```go
      // In signal_complete case, change the return to:
      r := &result{Status: "completed", NextLabel: &a.NextLabel, Message: &msg}
      if currentNotes != "" {
          r.Notes = &currentNotes
      }
      return "acknowledged", r

      // Same pattern for request_human
      ```

   ### Step 4: Pool — Persist Notes After Run

   **File to modify:** `backend/internal/agent/pool.go`

   In the `run()` method, after reading the result (around line 122, after `SetAgentRunCompleted`), add:
   ```go
   if result.Notes != nil && *result.Notes != "" {
       if _, err := p.q.UpdateTaskNotes(ctx, gen.UpdateTaskNotesParams{
           AgentNotes: *result.Notes,
           ID:         job.Input.Task.ID,
       }); err != nil {
           slog.Error("persist agent notes", "err", err)
       }
   }
   ```

   ### Step 5: Dispatcher — Inject Notes Into Prompt

   **File to modify:** `backend/internal/agent/dispatcher.go`
In the `dispatch()` method, after feedback reading (around line 106), add:
   ```go
   var agentNotes *string
   if t.AgentNotes != "" {
       agentNotes = &t.AgentNotes
   }
   ```

   Then set `PriorPlan: agentNotes` on the `RunInput` struct (around line 143).

   **File to modify:** `backend/internal/agent/claude.go`

   In `buildPrompt()`, change the header at line 225 from `"IMPLEMENTATION PLAN:\n"` to `"NOTES FROM PRIOR AGENT:\n"`.

   ### Step 6: Allowed Tools in ClaudeRunner

   **File to modify:** `backend/internal/agent/claude.go`

   At line 44, add `task-editor__update_task_notes` to the `allowedTools` string when MCP is configured:
   ```go
   allowedTools += ",task-editor__signal_complete,task-editor__request_human,task-editor__update_task_notes"
   ```

   ### Step 7: REST API Endpoint

   **File to modify:** `backend/internal/api/handlers/tasks.go`

Add `UpdateNotes` method:
   ```go
   func (h *TasksHandler) UpdateNotes(w http.ResponseWriter, r *http.Request) {
       var body struct {
           Notes  string `json:"notes"`
           Append bool   `json:"append"`
       }
       if err := decode(r, &body); err != nil {
           Err(w, http.StatusBadRequest, "invalid request body")
           return
       }
       taskID := chi.URLParam(r, "id")
       if body.Append {
           existing, err := h.q.GetTask(r.Context(), taskID)
           if err != nil {
               Err(w, http.StatusNotFound, "task not found")
               return
           }
           if existing.AgentNotes != "" {
               body.Notes = existing.AgentNotes + "\n\n" + body.Notes
           }
       }
       task, err := h.q.UpdateTaskNotes(r.Context(), gen.UpdateTaskNotesParams{
           AgentNotes: body.Notes,
           ID:         taskID,
       })
       if err != nil {
           Err(w, http.StatusInternalServerError, err.Error())
           return
       }
       JSON(w, http.StatusOK, task)
   }
   ```

   **File to modify:** `backend/internal/api/router.go`
 Add route at line 57 (after the existing task routes):
   ```go
   r.Patch("/tasks/{id}/notes", tasksH.UpdateNotes)
   ```

   ### Step 8: OpenAPI Spec

   **File to modify:** `openapi.yaml`

   1. Add `agent_notes: { type: string }` to the Task schema properties
   2. Add `/tasks/{id}/notes` PATCH endpoint definition

   ### Step 9: Frontend

   **File to modify:** `frontend/src/api/client.ts`

   1. Add `agent_notes?: string` to the `Task` type (around line 27)
   2. Add to `api.tasks` object:
      ```ts
      updateNotes: (id: string, notes: string, append = false) =>
        request<Task>(`/tasks/${id}/notes`, {
          method: 'PATCH',
          body: JSON.stringify({ notes, append }),
        }),
      ```

   **File to modify:** `frontend/src/pages/TaskDetailPage.tsx`

   In the left panel, after the description (around line 165), add an Agent Notes section:
   ```tsx
   {task.agent_notes && (
     <div>
       <p className="text-xs text-slate-500 mb-1">Agent Notes</p>
       <pre className="text-xs text-slate-300 bg-slate-800 rounded p-2 whitespace-pre-wrap max-h-60 overflow-y-auto">
         {task.agent_notes}
       </pre>
     </div>
   )}
   ```

### Step 10: Update CLAUDE.md Documentation

   Update:
   - `backend/cmd/mcp-server/CLAUDE.md` — add `update_task_notes` tool documentation
   - `backend/internal/agent/CLAUDE.md` — document notes flow
   - `backend/internal/storage/CLAUDE.md` — add migration 004 to the list
   - `backend/internal/api/handlers/CLAUDE.md` — add notes endpoint

   ---

   ## File Change Summary

   | File | Action | Description |
   |------|--------|-------------|
   | `backend/internal/storage/migrations/004_task_agent_notes.up.sql` | CREATE | Add `agent_notes` column |
   | `backend/internal/storage/migrations/004_task_agent_notes.down.sql` | CREATE | Rollback migration |
   | `backend/internal/storage/migrations/001_init.up.sql` | MODIFY | Add column to schema (for sqlc) |
   | `backend/internal/storage/queries/tasks.sql` | MODIFY | Add column to SELECTs; add `UpdateTaskNotes` query |
   | `backend/internal/storage/gen/*` | REGENERATE | Run `sqlc generate` |
   | `backend/internal/agent/provider.go` | MODIFY | Add `Notes *string` to `Result` |
   | `backend/cmd/mcp-server/main.go` | MODIFY | Add `update_task_notes` tool, notes accumulator, include notes in result |
   | `backend/internal/agent/pool.go` | MODIFY | Persist notes after run completion |
   | `backend/internal/agent/dispatcher.go` | MODIFY | Read `AgentNotes` from task, set on `RunInput.PriorPlan` |
   | `backend/internal/agent/claude.go` | MODIFY | Update prompt header; add tool to allowedTools |
   | `backend/internal/api/handlers/tasks.go` | MODIFY | Add `UpdateNotes` handler |
   | `backend/internal/api/router.go` | MODIFY | Add `/tasks/{id}/notes` route |
   | `openapi.yaml` | MODIFY | Add `agent_notes` to Task schema; add notes endpoint |
   | `frontend/src/api/client.ts` | MODIFY | Add `agent_notes` to `Task` type; add `updateNotes` method |
   | `frontend/src/pages/TaskDetailPage.tsx` | MODIFY | Add Agent Notes display section |
   | Various `CLAUDE.md` files | MODIFY | Document new feature |

   ## Verification Checklist

   - [ ] `cd backend && go test ./...` passes
   - [ ] `cd backend && sqlc generate` produces no errors
   - [ ] New migration applies cleanly on fresh DB
   - [ ] MCP sidecar responds to `update_task_notes` tool call
   - [ ] Notes appear in RESULT_FILE after agent exits
   - [ ] Pool persists notes to DB
   - [ ] Dispatcher injects notes into next agent's prompt
   - [ ] REST endpoint `PATCH /tasks/{id}/notes` works
   - [ ] Frontend shows agent notes on TaskDetailPage
   - [ ] Agent can call `update_task_notes` multiple times (append mode)
   PLAN_EOF
   