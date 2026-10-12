# 38. A repo verify command is authoritative over the agent's success claim

Status: Accepted

## Context

Success transitions were gated only on the agent's self-reported outcome
(`signal_complete` or a parsed `OUTCOME:` marker). Nothing checked it, and the
marker can be provoked from untrusted text (an imported issue body), so a
reviewer at the human gate had a diff and logs but no ground truth.

## Decision

- `repos.verify_command` / `verify_timeout_seconds` (default 600) configure an
  independent check. `workflow_labels.verify_mode` (`inherit` / `custom` /
  `disabled`) + `verify_command` override it per label (Josh chose
  "authoritative + per label"); the label is the task's dispatch-time label.
  The timeout is repo-wide.
- `Pool.run` calls `verifyOutcome` after `persistRunResult` and the safety-net
  commit and before `resolveOutcome`, only for a `completed` run with outcome
  `success`. Pass: nothing changes. Fail or timeout: the outcome is rewritten
  to `failure`, so the label's existing failure transition and the rework-loop
  breaker apply; with no failure path, `holdOnVerifyFailure` keeps the label,
  stores the feedback and counts toward `maxConsecutiveFailures`. Could not
  run (`error`): escalate to `waiting_human` (reason `verify_error`), never advance, never send back to the agent.
- Feedback is stored as the run's `feedback` beginning `VERIFICATION FAILED:`;
  `writeFeedbackSection` prints such feedback without the "prior review"
  header. The dispatcher already feeds the current run's feedback to the next
  run, so no new plumbing. Output is fenced and the closing fence stripped.
- Execution is the `agent.Verifier` seam (nil = fail closed), implemented by
  `providers.CommandVerifier`, which reuses `newCLICommand` (process group,
  `agentuser.Apply`), the provider env allow-list and `applyRuntime`: untrusted
  repo code gets the same isolation and toolchain as the agent CLI, never the
  server's capabilities or environment. It lives in `providers` because that is
  where the exec allow-list entry (`TestNoRawExecInServerCode`) and helpers are.
- Results: `run_verifications` (one row per run, cascade on run/task delete),
  `GET /tasks/{id}/verifications`, WS `task.verification`.

## Consequences

- A configured command overrides a forged or mistaken success claim; the cost
  is that long commands hold the pool worker (bounded by the timeout).
- Only `success` is verified, and only for pool-run agent outcomes; human
  moves and subtask merges are unaffected.
- Per-agent-config overrides, CI status of the pushed branch, and a dedicated
  escalation reason are deliberately left for follow-ups.
