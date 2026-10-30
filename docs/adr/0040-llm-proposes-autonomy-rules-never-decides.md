# 40. A model may propose autonomy rules and flags; it never decides a gate

Status: Accepted

## Context

The autonomy dial lets a human gate approve on its own. Each gate label has a
mode (`workflow_labels.autonomy_mode`):

- `ask`: always stop for a person.
- `on_flags`: approve unless one of the gate's `ask_on` review-brief flags is
  raised (or there is no diff to compute flags from).
- `auto`: approve unless a hard stop applies.

When a task lands on a gate, `internal/autonomy/policy` gathers an
`autonomy.Input` and calls `autonomy.Evaluate`, a pure function. The hard stops
it applies in both non-`ask` modes are `undone`, `always_ask`, a verify status
other than `pass`, cost over `autonomy_max_cost_usd`, and any matching ask rule
(`autonomy_ask_rules`). The decision and its reason codes go to
`autonomy_decisions`. An approval waits out the hold window, then moves the
task as actor `policy`.

People correct the policy after the fact. Undo, "Should have asked", a later
rejection and a reopen each write an `autonomy_escapes` row (source `undo`,
`should_have_asked`, `rejected_later`, `reopened`) linked to the decision. Enough
escapes in the window step the gate's mode down, and never up
(`autonomy/escape`). "Should have asked" can also create an ask rule, seeded by
`autonomy.Suggest`. The backtest endpoint replays a draft setting, including
draft `extra_rules`, against the gate's recent history.
[ADR 0038](0038-authoritative-verify-command.md) already treats an agent's
success claim as only a claim, and this policy is the second half of that
stance: nothing the agent says decides a gate.

Two questions keep coming up, and they need different answers.

1. **Should a gate ask a model for a risk score instead of applying fixed
   stops?** No; see the Decision. `auto` ignores review-brief *flags* but
   still applies every hard stop, so it is not a pass-through. Its weak case
   is a repo with no verify command and no `max_cost_usd`. There
   `VerifyStatus == ""`, so the verify stop cannot fire.

2. **Should a model help *write* ask rules and flags?** Yes, and this is the
   real gap. `autonomy.Suggest` builds rule conditions structurally from one
   task: deepest common directories, notable extensions (`SpecificExts`), task
   type, and a rounded size threshold. It works on one task by design and
   cannot see across tasks. So it cannot notice that six of the last nine
   rejections at a gate touched `*.sql`, or that every escape this month was a
   `chore` over 400 lines. Today nothing turns the accumulated escapes and
   rejections into a proposed rule. The operator has to see the pattern
   themselves.

## Decision

A model may **propose** autonomy rules and candidate flags. It never takes
part in evaluating a gate.

**The approve path stays pure.** `autonomy.Evaluate` keeps its signature and
its package does no I/O: no network, no database, no provider call. Its only
non-stdlib import stays `internal/pathglob`. Model output is reduced to the
existing deterministic rule shape (`path_globs`, `task_type`,
`min_changed_lines`, plus `repo_id`/`label` scope) before it can affect any
decision, and only after a human accepts it. An ask rule can only add a reason
to ask (`ReasonAskRule`). So a model can only ever cause *more* asking, never
less.

**Proposals are create-only and narrow.** A proposal is one new ask rule. A
model cannot propose deleting or loosening an existing rule (for example
raising its `min_changed_lines` or dropping a glob). It also cannot propose
changes to a gate's mode, `ask_on`, hold window, cost cap, a task's
`always_ask`, or the step-down settings. Loosening stays a human edit through
the existing rule and gate endpoints, because loosening is where a wrong
answer turns into a silent approval.

**Proposals come from history, offline.** A batch job reads one gate at a
time:

- its `autonomy_escapes` over a window, joined through `decision_id` to the
  decision and its reasons;
- the gate's human rejections (exits via the failure path, the same passes the
  backtest replays);
  moves by actor `pr-feedback-sync` (PR-feedback auto-transitions) are excluded,
  as are escapes they would cause;
- for each of those tasks: changed files, changed-line count, task type, repo,
  and the escape or rejection note.

It asks the model for candidate rules that would have stopped those tasks.
The window is 30 days, one entry per task (an escape wins over a rejection),
at most 40 entries. Escape facts are replayed as of the policy approval, when
a rule would have fired. It runs on a schedule (every
`proposal_interval_hours`, default 24, for gates not in `ask` mode, where a
rule would change nothing) or on demand for any human gate, never on a task's
landing path. So model latency, rate limits and provider outages cannot delay
a transition. Every run is a row in `autonomy_proposal_runs` with its status,
error, cost and tokens. A failed run leaves the previous proposals in place.
A gate with fewer than 3 escapes plus rejections in the window is `skipped`
without a model call: a pattern needs more than one example, and the
single-task case already has `Suggest`.

**Model output is untrusted input.** Task titles, notes and file paths come
partly from third parties (imported issues, review comments). They are fenced
in the prompt as data, as in [ADR 0014](0014-untrusted-content-fencing.md).
The response is parsed as strict JSON into `AskRuleInput` and must pass
`AskRule.Validate`: at least one condition, valid lesson-syntax globs, at most
20 globs, `min_changed_lines >= 1`, and a known repo and label. Anything else
is dropped, not repaired. Even a successful injection can produce only an
extra-asking proposal that a human still has to accept.

**Evidence is computed by the server, not claimed by the model.** The model is
not asked which tasks a rule covers. The server replays every candidate over
all of the gate's escapes and rejections in the window with the same matcher
`Evaluate` uses (`AskRule.Matches` on `Facts` recomputed as of each pass, as
the backtest does), and stores the tasks it really matches. A proposal that
catches none of them is discarded. Each proposal that survives is shown with
two numbers from that replay:

- **caught**: how many of the gate's escapes and rejections it would have
  stopped, with links to those tasks;
- **cost**: how many policy approvals that were *not* later escaped it would
  also have stopped. This is the extra asking it buys, the same replay as the
  `would_stop` side of `POST /workflows/{id}/labels/{label}/autonomy/backtest`
  with the rule as `extra_rules`.

Tasks whose diff is gone are counted as **unknown** for path and size
conditions, never as caught.

If the operator cannot check a proposal against real history, it is not shown.

**Proposals are inert until accepted.** They live in their own table,
`autonomy_rule_proposals`, never directly in `autonomy_ask_rules`. A row holds
the gate, the rule conditions, the evidence task ids, the replay counts, the
model's one-line rationale (shown as untrusted text), the provider config and
the run that produced it, and a status of `proposed`, `accepted` or `dismissed`.
Accepting a proposal creates a normal ask rule with the same validation and
`CreateAskRule` query as `POST /autonomy/ask-rules`. `created_by` is the person who
accepted it (`ActorFromContext`, as today), and the proposal row records the
new rule's id. The rule then behaves like any other: it counts hits, can be
edited or deleted, and appears in gate history. A dismissal is remembered
against the normalized conditions (sorted globs, task type, threshold, scope),
so the same proposal is not offered again. A later run with new evidence for
the same conditions only updates the counts on the dismissed row.

**The provider is an existing provider config.** The feature reaches the model
through a nullable `proposal_provider_config_id` column on the
`autonomy_settings` singleton (migration 085). These are the same provider
configs that back agent configs and chat sessions
([ADR 0018](0018-provider-configs-and-provider-deprecation.md)), so keys and
model choice already work. Unset means the feature is off and nothing calls
out. The repo has no direct model API (the `anthropic`/`llm` providers were
removed), so a proposal run is one run of the provider's CLI outside any task,
like the rate-limit reset check: an empty scratch directory, no MCP sidecar,
shell commands denied, and the answer read from a marked JSON block in the
output. Its spend is recorded on the run row, not on any task.

**Flag codes stay a closed set.** `internal/reviewbrief` produces the `ask_on`
codes, and `Config.Validate` checks them against the known set. A model may
report that a *new* flag code looks worth implementing, for example "most
escapes added a dependency, and no flag covers lockfile changes outside
`*.lock`". That is a note for a person to read, stored on the run and shown
under the gate's suggestions. It cannot add a flag at runtime, and no proposal
can reference a flag that does not exist.

**Rejected alternatives.**

- *Model risk score inside `Evaluate`* (a threshold on a model's 0–1 risk).
  This puts a network call and nondeterminism on the approve path. It makes
  backtests cost money and give different answers on each replay, and it turns
  reason codes into "the model said 0.31". It also reverses the asymmetry: a
  low score would cause *less* asking.
- *Model as an extra hard stop only* (it can veto but never approve). This is
  safer in direction, but it still couples transitions to provider uptime and
  latency, and still makes backtests non-reproducible. It also gives the human
  no rule to read or edit afterwards. If a model sees a pattern, the useful
  output is the rule, not a one-off veto.
- *Auto-accepting high-confidence proposals.* This removes the human step that
  keeps the system explainable, and the model's own confidence is the one
  input we are explicitly not trusting. Step-down already gives an automatic
  tightening path that needs no model.
- *Extending `Suggest` with cross-task heuristics instead of a model.* This is
  worth doing for the obvious cases (a shared extension across N escapes), and
  it can feed the same proposal table with `created_by` set to a heuristic
  source. It does not replace a model for patterns that need the notes ("all
  of these broke the migration ordering").

## Consequences

- Backtests keep working and keep meaning what they say. Replaying history
  stays free, fast and reproducible, because no step in a decision needs a
  provider call.
- Decisions stay explainable as reason codes (`ask_rule` + `rule_id`), not as
  a score. Label history and `autonomy_decisions` need no new shape. A rule
  that came from a proposal can be traced back to it through the proposal
  row.
- The feature is purely additive. With no `provider_config_id` configured,
  behavior is exactly today's, and the new table stays empty.
- A model's opinion changes nothing until two things happen: a human accepts
  it, and the normal rule-creation path runs. That is deliberate; it is what
  keeps the asymmetry true (proposals can only add asking).
- A proposal can be wrong without being dangerous. The worst cases are a noisy
  suggestion that a human dismisses, or an over-broad rule (say `**`) that
  causes extra asking and shows a large *cost* number before anyone accepts
  it. Neither is a silent approval.
- Cost is bounded and attributable: batch, per gate, on a schedule, against a
  named provider config, and skipped for gates without enough history. It is
  never one call per gate landing.
- The surface is `internal/autonomy/propose` (gather, prompt, parse, replay,
  store, an hourly scheduler), migration 085 (`autonomy_rule_proposals`,
  `autonomy_proposal_runs`, two settings columns), the
  `/autonomy/rule-proposals` and `.../autonomy/proposals/run|proposal-runs`
  endpoints, and a "Suggested ask rules" section in the gate panel. None of it
  touches `autonomy.Evaluate`, `policy`, or `escape`.
- Accepted rules from proposals feed the same escape and hit-count data that
  the next batch reads. Hit counts are visible, so a rule that never fires can
  be pruned by hand. The job must not re-propose a rule that already exists
  (same normalized conditions and scope).
- This does not fix the weak `auto` case from Context (no verify command, no
  cost cap). Whether a missing verify command should itself be a hard stop
  under `auto` is a separate decision. Today the UI only warns.
