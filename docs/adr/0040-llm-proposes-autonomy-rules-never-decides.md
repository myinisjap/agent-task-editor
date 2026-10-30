# 40. A model may propose autonomy rules and flags; it never decides a gate

Status: Proposed

## Context

Autonomy gates are evaluated by `internal/autonomy.Evaluate`, a pure function
over a deterministic review brief. [ADR 0038](0038-authoritative-verify-command.md)
already establishes that an agent's success claim is only a claim; the gate
policy is the second half of that posture.

Two separate questions keep coming up, and they deserve different answers:

1. **Should `auto` call a model for a risk score instead of applying fixed
   stops?** No — see the Decision. `auto` ignores review-brief *flags*, but
   still applies the hard stops (`undone`, `always_ask`, verify not `pass`,
   cost over the cap, any matching ask rule), so it is not a pass-through.
   Its weak case is a repo with no verify command and no `max_cost_usd`, where
   `VerifyStatus == ""` means the verify stop cannot fire.

2. **Should a model help *write* ask rules and flags?** Yes. This is where the
   real gap is. `autonomy.Suggest` derives rule conditions structurally from
   one task: deepest common directories, notable extensions, task type, a
   rounded size threshold. It is per-task by construction and cannot see
   across tasks, so it cannot notice that six of the last nine rejections at a
   gate touched `*.sql`. Nothing today turns accumulated `autonomy_escapes`
   and rejections into a proposed rule.

## Decision

A model may **propose** autonomy rules and candidate flags. It may never
participate in evaluating a gate.

**The approve path stays pure.** `autonomy.Evaluate` keeps its current
signature and stays stdlib-only, with no I/O and no provider call. Whatever a
model produces is reduced to the existing deterministic rule shape
(`path_globs`, `task_type`, `min_changed_lines`) before it can affect any
decision, and only after a human accepts it. Consequently a model can only
ever cause *more* asking, never less.

**Proposals come from history, offline.** A batch job reads
`autonomy_escapes` (joined to its `decision_id`) and the gate's rejected
passes over a window, groups them, and asks the model for candidate rules that
would have caught them. It runs on a schedule or on demand — never on the
landing path of a task — so latency, rate limits and provider outages cannot
delay a transition. A failure is logged and leaves the previous proposals in
place.

**Proposals are inert until accepted.** They are stored in their own table
with a status (`proposed` / `accepted` / `dismissed`), never written directly
into `autonomy_ask_rules`. Accepting one creates a normal ask rule through the
existing `POST /autonomy/ask-rules` path, with `created_by` recording that it
came from a proposal. Dismissal is remembered so the same proposal is not
re-offered.

**Every proposal must carry its evidence.** A proposal names the specific past
tasks it was derived from and is backtestable before acceptance: the existing
`extra_rules` field on
`POST /workflows/{id}/labels/{label}/autonomy/backtest` already replays a
draft rule against recent policy approvals and reports `would_stop`. A
proposal the operator cannot check against real history is not shown.

**The provider is an existing provider config.** The model is reached through
a `provider_config_id` on the `autonomy_settings` singleton (migration 084),
the same configs that back agent configs and chat sessions
([ADR 0018](0018-provider-configs-and-provider-deprecation.md)), so keys,
model selection and cost attribution already work. Unset = the feature is off
and nothing calls out.

**Flag codes stay a closed set.** `ask_on` codes are produced by
`internal/reviewbrief` and validated server-side. A model may propose that a
*new* flag code is worth implementing, as a report to a human; it cannot
introduce a flag at runtime.

## Consequences

- Backtest keeps working and keeps meaning what it says. Replaying history
  stays free, fast and reproducible, because no step in a decision requires a
  provider call.
- Decisions remain explainable as reason codes (`ask_rule` + `rule_id`), not
  as a score. Label history and `autonomy_decisions` need no new shape.
- The feature is strictly additive: with no `provider_config_id` configured,
  behavior is exactly today's.
- Two round trips are required before a model's opinion changes anything — a
  human tick, then the normal rule-creation path. That is deliberate; it is
  what keeps the asymmetry (proposals can only add asking) true.
- A proposal can be wrong without being dangerous. The worst case is a noisy
  suggestion a human declines, or an over-broad rule that causes extra asking
  — never a silent approval.
- Cost is bounded and attributable: batch, on a schedule, against a named
  provider config, rather than one call per gate landing.
- This does not address the weak `auto` case noted in Context (no verify
  command, no cost cap). That is a separate decision about whether an absent
  verify command should itself be a hard stop under `auto`.
