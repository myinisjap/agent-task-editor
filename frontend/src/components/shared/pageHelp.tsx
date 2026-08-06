// Centralized help-modal copy for every page. Each export is the <section>
// children rendered inside a <HelpModal title="…">…</HelpModal> on that
// page. Keeping copy here avoids bloating each page component.

const EXAMPLE_WORKFLOW_YAML = `name: My Workflow
description: Optional description
labels:
  - name: backlog
    color: "#6B7280"
    sort_order: 0
    agent_ignore: true
  - name: in-progress
    color: "#F59E0B"
    sort_order: 1
  - name: done
    color: "#10B981"
    sort_order: 2
    is_terminal: true
transitions:
  - from: backlog
    to: in-progress
    trigger: human
  - from: in-progress
    to: done
    trigger: agent
    path: success
  - from: done
    to: in-progress
    trigger: human`

export function WorkflowHelp() {
  return (
    <>
      <section className="flex flex-col gap-1.5">
        <h3 className="text-slate-100 font-semibold">Overview</h3>
        <p>
          A workflow is a state machine made up of <strong>labels</strong> (the columns on the board)
          and <strong>transitions</strong> (the allowed moves between labels). Tasks start on a label
          and can only move to another label if a matching transition is defined — any other move is
          rejected.
        </p>
      </section>

      <section className="flex flex-col gap-1.5">
        <h3 className="text-slate-100 font-semibold">Labels</h3>
        <ul className="flex flex-col gap-1 list-disc list-inside">
          <li><code className="bg-slate-800 rounded px-1 font-mono">name</code> — unique identifier within the workflow</li>
          <li><code className="bg-slate-800 rounded px-1 font-mono">color</code> — hex color used on the board</li>
          <li><code className="bg-slate-800 rounded px-1 font-mono">sort_order</code> — column order on the board</li>
          <li><code className="bg-slate-800 rounded px-1 font-mono">agent_ignore</code> — agents cannot move tasks here; the dispatcher skips tasks already on this label</li>
          <li><code className="bg-slate-800 rounded px-1 font-mono">is_terminal</code> — marks the task as complete; no further transitions</li>
          <li><code className="bg-slate-800 rounded px-1 font-mono">create_pr</code> — entering this label pushes the branch and auto-opens a GitHub PR (needs a GitHub remote and gh auth); at most one label per workflow may set this</li>
        </ul>
        <p>
          Tasks created without an explicit label — GitHub Issue imports, scheduled tasks, and API
          creates that omit one — land on the workflow's <strong>human-gate label</strong>: the
          lowest <code className="bg-slate-800 rounded px-1 font-mono">sort_order</code>{' '}
          <code className="bg-slate-800 rounded px-1 font-mono">agent_ignore</code> label (falling back
          to the first label if none is marked <code className="bg-slate-800 rounded px-1 font-mono">agent_ignore</code>),
          so a human promotes them before an agent picks them up. There is no reserved label name — in
          the default workflow this happens to be <code className="bg-slate-800 rounded px-1 font-mono">not_ready</code>.
        </p>
      </section>

      <section className="flex flex-col gap-1.5">
        <h3 className="text-slate-100 font-semibold">Transitions</h3>
        <ul className="flex flex-col gap-1 list-disc list-inside">
          <li><code className="bg-slate-800 rounded px-1 font-mono">from</code> / <code className="bg-slate-800 rounded px-1 font-mono">to</code> — source and destination label names</li>
          <li><code className="bg-slate-800 rounded px-1 font-mono">trigger</code> — <code className="bg-slate-800 rounded px-1 font-mono">agent</code>, <code className="bg-slate-800 rounded px-1 font-mono">human</code>, or <code className="bg-slate-800 rounded px-1 font-mono">both</code>: who is allowed to make this move</li>
          <li><code className="bg-slate-800 rounded px-1 font-mono">path</code> — <code className="bg-slate-800 rounded px-1 font-mono">success</code> or <code className="bg-slate-800 rounded px-1 font-mono">failure</code>: which outcome this transition represents, used by the Approve/Reject actions</li>
        </ul>
        <p>Only transitions you define are allowed — a task can never skip to a label with no matching transition.</p>
      </section>

      <section className="flex flex-col gap-1.5">
        <h3 className="text-slate-100 font-semibold">YAML Example</h3>
        <pre className="bg-slate-800 rounded p-3 font-mono text-xs text-slate-200 overflow-x-auto whitespace-pre">
{EXAMPLE_WORKFLOW_YAML}
        </pre>
      </section>

      <p className="text-xs text-slate-500">
        For the full reference — including the default workflow, Approve/Reject semantics, and engine
        rules — see <code className="bg-slate-800 rounded px-1 font-mono">docs/workflows.md</code> in the repo.
      </p>
    </>
  )
}

export function DashboardHelp() {
  return (
    <>
      <section className="flex flex-col gap-1.5">
        <h3 className="text-slate-100 font-semibold">Overview</h3>
        <p>
          This is the at-a-glance summary of the active workflow: how many tasks sit on each
          label, which agents are currently running, and which tasks are waiting on you.
        </p>
      </section>
      <section className="flex flex-col gap-1.5">
        <h3 className="text-slate-100 font-semibold">Needs your input</h3>
        <p>
          Tasks land here when a human-triggered transition (e.g. Approve/Reject a plan or a
          finished review) is waiting on you. Approve moves the task forward on its{' '}
          <code className="bg-slate-800 rounded px-1 font-mono">success</code> path; Reject
          requires a note and moves it on its{' '}
          <code className="bg-slate-800 rounded px-1 font-mono">failure</code> path.
        </p>
      </section>
      <section className="flex flex-col gap-1.5">
        <h3 className="text-slate-100 font-semibold">Visualize tasks</h3>
        <p>
          A fun, non-essential toggle that renders the label counts as an office floor, a robot
          crew, or a factory assembly line instead of plain count chips. Purely cosmetic — it has
          no effect on tasks or agents.
        </p>
      </section>
    </>
  )
}

export function UsageHelp() {
  return (
    <>
      <section className="flex flex-col gap-1.5">
        <h3 className="text-slate-100 font-semibold">Claude usage</h3>
        <p>
          Live utilization of your Claude account's 5-hour and weekly rate-limit windows, as
          reported by the <code className="bg-slate-800 rounded px-1 font-mono">claude</code>{' '}
          CLI provider. Only shown when at least one run has reported this data.
        </p>
      </section>
      <section className="flex flex-col gap-1.5">
        <h3 className="text-slate-100 font-semibold">Cost estimates</h3>
        <p>
          Per-provider and per-model cost breakdowns, estimated from recorded token usage. The{' '}
          <code className="bg-slate-800 rounded px-1 font-mono">claude</code> and{' '}
          <code className="bg-slate-800 rounded px-1 font-mono">qwen_code</code> CLIs report their
          own authoritative cost; the <code className="bg-slate-800 rounded px-1 font-mono">anthropic</code>{' '}
          and <code className="bg-slate-800 rounded px-1 font-mono">llm</code> providers are
          estimated from the price table you configure on the <strong>Pricing</strong> page.
        </p>
      </section>
    </>
  )
}

export function PerformanceHelp() {
  return (
    <>
      <section className="flex flex-col gap-1.5">
        <h3 className="text-slate-100 font-semibold">Outcome quality</h3>
        <p>
          <strong>Success rate measures whether a run exited cleanly — it does not measure whether
          the work stuck.</strong> A config with a high success rate but a high rework rate finishes
          confidently and then gets bounced back; that's worse than a config with a lower success
          rate but little rework. These metrics answer "did the work stick":
        </p>
        <ul className="list-disc list-inside space-y-1">
          <li>
            <strong>Cost to done</strong> — average total spend across every run of a task
            (including failed and retried ones), from creation until it reached a terminal label.
          </li>
          <li>
            <strong>Rework rate</strong> — the percentage of finished tasks that moved backward
            into a label they'd already occupied at least once (bounced back for more work),
            attributed to whichever run caused the bounce-back.
          </li>
          <li>
            <strong>Human-touch rate</strong> — the percentage of finished tasks that needed at
            least one human-triggered move along the way, not just a final approval.
          </li>
          <li>
            <strong>Review burden</strong> — average review comments received per finished task.
          </li>
          <li>
            <strong>Escalation rate</strong> — the percentage of a config's finished runs that
            ended waiting on a human instead of completing.
          </li>
        </ul>
        <p>
          Every rate is shown with its sample size (n); rates computed from fewer than 10 tasks or
          runs are flagged as low-sample and should be treated with caution — a config with 2 tasks
          at 100% is not more reliable than one with 200 tasks at 85%. Use the repo filter to check
          whether a config's numbers hold up on a specific codebase, since aggregate numbers can
          hide a config that's excellent on one repo and poor on another.
        </p>
      </section>
      <section className="flex flex-col gap-1.5">
        <h3 className="text-slate-100 font-semibold">Agent config performance</h3>
        <p>
          Aggregated stats per agent config: number of runs, success rate, average/P90 run
          duration, and average turns per task. Success rate is still useful for spotting a flaky
          or crashing config, but it's a weaker signal than the outcome-quality metrics above —
          use it alongside them, not instead of them.
        </p>
      </section>
    </>
  )
}

export function BoardHelp() {
  return (
    <>
      <section className="flex flex-col gap-1.5">
        <h3 className="text-slate-100 font-semibold">Columns &amp; cards</h3>
        <p>
          Each column is a workflow <strong>label</strong>. Dragging a card to another column
          attempts the matching <strong>transition</strong> — only moves defined in the active
          workflow are allowed; anything else is rejected. Use the workflow picker in the header
          to switch which workflow's board you're viewing.
        </p>
      </section>
      <section className="flex flex-col gap-1.5">
        <h3 className="text-slate-100 font-semibold">Agents pick up work automatically</h3>
        <p>
          Any label with an agent-triggered transition is watched by the dispatcher: once a task
          lands there (and the label isn't marked <code className="bg-slate-800 rounded px-1 font-mono">agent_ignore</code>),
          an agent run is created automatically using the agent config bound to that label. Human
          gated columns wait for you to Approve/Reject instead.
        </p>
      </section>
      <section className="flex flex-col gap-1.5">
        <h3 className="text-slate-100 font-semibold">Filters &amp; bulk actions</h3>
        <p>
          Use the search box and repo/type/git-state filters to narrow the board. Select multiple
          cards (shift-click for a range) to pause, resume, archive, or move them together via the
          bulk action bar.
        </p>
      </section>
      <section className="flex flex-col gap-1.5">
        <h3 className="text-slate-100 font-semibold">Creating a task</h3>
        <p>
          Use "+ Add Task" on the board to open the new-task form. You choose the repo, workflow,
          type, and optionally start from a saved template (see the <strong>Templates</strong>{' '}
          page).
        </p>
      </section>
    </>
  )
}

export function ChatHelp() {
  return (
    <>
      <section className="flex flex-col gap-1.5">
        <h3 className="text-slate-100 font-semibold">What this is</h3>
        <p>
          An interactive terminal session with an AI provider, running against a repo of your
          choice — useful for ad-hoc exploration, debugging, or one-off requests outside the
          task/workflow pipeline. Sessions are independent of tasks: nothing here moves a task
          between labels or triggers the dispatcher.
        </p>
      </section>
      <section className="flex flex-col gap-1.5">
        <h3 className="text-slate-100 font-semibold">Starting a session</h3>
        <p>
          Pick a repo and a provider config in "New terminal", then start chatting. Each session
          keeps its own scrollback and can be resumed later from the session list on the left.
        </p>
      </section>
      <section className="flex flex-col gap-1.5">
        <h3 className="text-slate-100 font-semibold">On mobile</h3>
        <p>
          A key bar sits under the terminal with the keys a phone keyboard lacks — Esc, Tab,
          Shift+Tab (mode switching in the Claude CLI), arrows, Home/End and page up/down. Scroll
          it sideways for more. <strong>Ctrl</strong> is sticky: tap it, then type a letter on your
          keyboard to send Ctrl+that key (Ctrl+R, Ctrl+A, …); tap it again to cancel.
        </p>
      </section>
    </>
  )
}

export function AgentsHelp() {
  return (
    <>
      <section className="flex flex-col gap-1.5">
        <h3 className="text-slate-100 font-semibold">What an agent config is</h3>
        <p>
          An agent config binds a set of workflow <strong>labels</strong> to a{' '}
          <strong>provider config</strong> (see the Providers page) plus its own settings: system
          prompt, token/turn limits, timeout, retries, and optional plugins/MCP servers or command
          allow/deny lists. When a task lands on one of its labels, the dispatcher uses this config
          to run the agent.
        </p>
      </section>
      <section className="flex flex-col gap-1.5">
        <h3 className="text-slate-100 font-semibold">Labels &amp; transitions</h3>
        <p>
          The labels you assign here must exist in a workflow, and only labels reachable by an{' '}
          <code className="bg-slate-800 rounded px-1 font-mono">agent</code>-triggered transition
          actually get picked up automatically — configure the workflow's transitions on the{' '}
          <strong>Workflows</strong> page.
        </p>
      </section>
      <section className="flex flex-col gap-1.5">
        <h3 className="text-slate-100 font-semibold">Templates</h3>
        <p>
          Use "Templates" in the sidebar to start from a pre-filled config (planner, coder,
          reviewer, etc.) instead of building one from scratch.
        </p>
      </section>
    </>
  )
}

export function ProvidersHelp() {
  return (
    <>
      <section className="flex flex-col gap-1.5">
        <h3 className="text-slate-100 font-semibold">What a provider config is</h3>
        <p>
          A provider config is the reusable provider/model/credentials triple — which provider CLI
          or API to use, which model, and the environment variables (e.g. an API key) needed to
          authenticate it. Agent configs and Chat sessions reference a provider config by id, so
          the same credentials can be shared across many agents instead of duplicated.
        </p>
      </section>
      <section className="flex flex-col gap-1.5">
        <h3 className="text-slate-100 font-semibold">Provider types</h3>
        <ul className="flex flex-col gap-1 list-disc list-inside">
          <li><code className="bg-slate-800 rounded px-1 font-mono">claude</code> — the Claude Code CLI, authenticated via its own login/session</li>
          <li><code className="bg-slate-800 rounded px-1 font-mono">qwen_code</code>, <code className="bg-slate-800 rounded px-1 font-mono">codex_cli</code>, <code className="bg-slate-800 rounded px-1 font-mono">opencode</code> — other CLI-based providers</li>
        </ul>
        <p>
          <code className="bg-slate-800 rounded px-1 font-mono">anthropic</code> (direct Anthropic API calls) and{' '}
          <code className="bg-slate-800 rounded px-1 font-mono">llm</code> (any OpenAI-compatible API) are{' '}
          <strong>deprecated and disabled for new configs</strong> — they're no longer offered here, but existing
          configs on them keep running.
        </p>
      </section>
      <section className="flex flex-col gap-1.5">
        <h3 className="text-slate-100 font-semibold">Checking readiness</h3>
        <p>
          After saving a config, check the <strong>Health</strong> page to confirm its credentials
          and CLI/API are reachable before assigning it to an agent.
        </p>
      </section>
    </>
  )
}

export function PricingHelp() {
  return (
    <>
      <section className="flex flex-col gap-1.5">
        <h3 className="text-slate-100 font-semibold">What this configures</h3>
        <p>
          USD price per 1M input/output tokens, used to estimate run cost for the{' '}
          <code className="bg-slate-800 rounded px-1 font-mono">anthropic</code> and{' '}
          <code className="bg-slate-800 rounded px-1 font-mono">llm</code> providers. The{' '}
          <code className="bg-slate-800 rounded px-1 font-mono">claude</code> and{' '}
          <code className="bg-slate-800 rounded px-1 font-mono">qwen_code</code> CLIs report their
          own authoritative cost and are unaffected by this table.
        </p>
      </section>
      <section className="flex flex-col gap-1.5">
        <h3 className="text-slate-100 font-semibold">Fallback behavior</h3>
        <p>
          A model with no row here falls back to an internal, approximate hardcoded table. A run
          whose model matches neither is flagged "cost unknown" in its run history rather than
          silently showing $0. Changes take effect on the next run — no restart needed.
        </p>
      </section>
    </>
  )
}

export function ReposHelp() {
  return (
    <>
      <section className="flex flex-col gap-1.5">
        <h3 className="text-slate-100 font-semibold">Registering a repo</h3>
        <p>
          A repo is a local directory path that agents run against — their working directory is
          set to this path. If <code className="bg-slate-800 rounded px-1 font-mono">REPO_BASE_DIR</code>{' '}
          is set on the server, only paths inside it can be registered.
        </p>
      </section>
      <section className="flex flex-col gap-1.5">
        <h3 className="text-slate-100 font-semibold">GitHub integration</h3>
        <p>
          Set a <code className="bg-slate-800 rounded px-1 font-mono">remote_url</code> pointing at
          GitHub to enable PR auto-open on a workflow's{' '}
          <code className="bg-slate-800 rounded px-1 font-mono">create_pr</code> label, issue import,
          and PR-review auto-transition. These need <code className="bg-slate-800 rounded px-1 font-mono">gh</code>{' '}
          auth (<code className="bg-slate-800 rounded px-1 font-mono">gh auth login</code> or{' '}
          <code className="bg-slate-800 rounded px-1 font-mono">GITHUB_TOKEN</code>) — check the{' '}
          <strong>Health</strong> page to confirm it's configured.
        </p>
      </section>
      <section className="flex flex-col gap-1.5">
        <h3 className="text-slate-100 font-semibold">Issue sync</h3>
        <p>
          Turning on "Import GitHub Issues" periodically creates board tasks from open issues
          (optionally filtered to one label) in the repo's assigned workflow — and keeps them in
          sync afterward. If the issue's title, body, or labels change upstream, the task is
          updated to match (subject to the update policy below); if the issue closes or loses the
          filter label, the task is flagged rather than silently left behind.
        </p>
        <p>
          The label filter here is deprecated in favor of <strong>Intake Rules</strong>{' '}
          (Configuration → Intake Rules), which can match on more than one label and shape the
          resulting task (template, priority, target label, cost budget) rather than just
          filtering which issues come in. It's still honoured for one more release; existing
          values were migrated into equivalent rules automatically.
        </p>
      </section>
      <section className="flex flex-col gap-1.5">
        <h3 className="text-slate-100 font-semibold">Keeping tasks in sync (update policy)</h3>
        <p>
          <code className="bg-slate-800 rounded px-1 font-mono">issue_sync_update_policy</code>{' '}
          controls when upstream edits are allowed to overwrite the task:
        </p>
        <ul className="flex flex-col gap-1 list-disc list-inside">
          <li><strong>gate</strong> (default) — only while the task is still in the workflow's human-gate column; once it moves past that, the task is frozen even if the issue keeps changing.</li>
          <li><strong>always</strong> — upstream edits apply no matter where the task currently sits.</li>
          <li><strong>never</strong> — drift is still detected, but nothing is written to the task.</li>
        </ul>
      </section>
      <section className="flex flex-col gap-1.5">
        <h3 className="text-slate-100 font-semibold">Closed or unlabeled issue action</h3>
        <p>
          <code className="bg-slate-800 rounded px-1 font-mono">issue_sync_gone_action</code>{' '}
          decides what happens when the source issue closes or stops matching the label filter:
        </p>
        <ul className="flex flex-col gap-1 list-disc list-inside">
          <li><strong>flag</strong> (default) — just marks the task so you can see it needs a look; no other action taken.</li>
          <li><strong>archive</strong> — also archives the task.</li>
          <li><strong>move</strong> — also moves the task to <code className="bg-slate-800 rounded px-1 font-mono">issue_sync_gone_label</code>.</li>
        </ul>
        <p>
          A task with a currently running agent is always just flagged, never archived or moved —
          it's re-checked on the next sweep once the run finishes.
        </p>
      </section>
      <section className="flex flex-col gap-1.5">
        <h3 className="text-slate-100 font-semibold">Issue comment sync</h3>
        <p>
          <code className="bg-slate-800 rounded px-1 font-mono">issue_comment_sync_enabled</code>{' '}
          (off by default) ingests the source issue's comment thread onto the task, so the
          pre-work discussion isn't invisible to the agent. Only comments from people with write
          access to the repo are ingested, and this app's own write-back comments are always
          filtered out. Comments follow the same update policy as field sync above (frozen once
          the task leaves the gate column, by default). Ingested comments are passed to the agent
          as clearly marked untrusted context — never treated as instructions.
        </p>
      </section>
    </>
  )
}

export function TemplatesHelp() {
  return (
    <>
      <section className="flex flex-col gap-1.5">
        <h3 className="text-slate-100 font-semibold">What templates are for</h3>
        <p>
          A template is a reusable title/description/type preset for recurring shapes of work
          (e.g. "Upgrade dependency"). It pre-fills the "New Task" form so you don't retype the
          same boilerplate every time.
        </p>
      </section>
      <section className="flex flex-col gap-1.5">
        <h3 className="text-slate-100 font-semibold">Recurring schedules</h3>
        <p>
          A template can also be attached to a cron schedule against a repo, so a new task is
          created automatically on that schedule — no human has to click "New Task". Deleting a
          template also deletes any schedules that use it.
        </p>
      </section>
    </>
  )
}

export function IntakeRulesHelp() {
  return (
    <>
      <section className="flex flex-col gap-1.5">
        <h3 className="text-slate-100 font-semibold">What intake rules are for</h3>
        <p>
          A rule matches an incoming imported issue or a firing schedule (source, repo, labels,
          title/body pattern, issue author association) and applies shaping to the task it
          creates: a template, priority, target label, workflow override, and/or cost budget.
          Rules are evaluated first-match-wins, in the order shown below — the first enabled rule
          whose conditions all hold wins, and no rule after it is considered.
        </p>
      </section>
      <section className="flex flex-col gap-1.5">
        <h3 className="text-slate-100 font-semibold">The auto-start safety gate</h3>
        <p>
          Imported issues land on the workflow's human-review gate label by default, so a person
          promotes them before an agent ever runs — the mitigation for untrusted imported issue
          content. A rule can target a different, agent-triggerable label instead (skipping that
          review step), but only when it also restricts the issue author's association to
          OWNER, MEMBER, or COLLABORATOR. The form blocks saving a rule that would auto-start
          without that constraint.
        </p>
      </section>
      <section className="flex flex-col gap-1.5">
        <h3 className="text-slate-100 font-semibold">issue_sync_label is deprecated</h3>
        <p>
          The old per-repo "only import issues with this label" setting still controls which
          issues are fetched for one more release, but an intake rule with a matching
          match_labels condition is the way to both filter and shape new work going forward.
          Existing issue_sync_label values were migrated into equivalent rules automatically.
        </p>
      </section>
      <section className="flex flex-col gap-1.5">
        <h3 className="text-slate-100 font-semibold">Preview</h3>
        <p>
          "Preview matches" checks a rule (saved or still being edited) against the repo's most
          recently imported tasks, using the exact same matching logic the importer and
          scheduler use at runtime — so what you see previewed is what will actually happen.
        </p>
      </section>
    </>
  )
}

export function HealthHelp() {
  return (
    <>
      <section className="flex flex-col gap-1.5">
        <h3 className="text-slate-100 font-semibold">What this checks</h3>
        <p>
          Readiness of each configured provider and supporting infrastructure — e.g. the Claude
          CLI login, API keys, GitHub auth for PRs/issue sync, and the configured repo base
          directory. Green means ready; yellow/red need attention before you run a task with that
          provider.
        </p>
      </section>
      <section className="flex flex-col gap-1.5">
        <h3 className="text-slate-100 font-semibold">Fixing a red/yellow row</h3>
        <p>
          Follow the message shown on the row — it usually points at a missing credential, an
          unauthenticated CLI, or a misconfigured path. Re-run "Refresh" after fixing it to
          confirm.
        </p>
      </section>
    </>
  )
}
