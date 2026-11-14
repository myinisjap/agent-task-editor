// Package automerge implements the opt-in per-repo auto-merge policy: when a
// task's open PR is safe to merge (checks green, no conflicts, no outstanding
// "changes requested", nobody working on the task) ATE merges it through the
// forge itself, and ghsync's normal merged-PR handling then finishes the task.
//
// The package has three parts:
//   - Evaluate / Precheck: a pure eligibility decision over already-fetched PR
//     facts (table-tested).
//   - Reconcile: run by ghsync on each PR refresh; fetches what Evaluate needs,
//     records the verdict in task_auto_merge (for the UI) and enqueues an
//     auto_merge outbox side effect when the PR is eligible.
//   - Merge: the outbox hook body; re-evaluates against live forge state and
//     merges, pinned to the evaluated head SHA.
package automerge

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/myinisjap/agent-task-editor/backend/internal/forge"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
	"github.com/myinisjap/agent-task-editor/backend/internal/workflow"
	"github.com/myinisjap/agent-task-editor/backend/internal/wsevent"
)

// Repo auto_merge_mode values.
const (
	ModeOff       = "off"
	ModeWhenGreen = "when_green"
)

// Task auto-merge states stored in task_auto_merge.state.
const (
	StateWaiting = "waiting" // blocked; Reason/Message say why
	StateMerging = "merging" // eligible; merge queued in the outbox
)

// Reason codes for a blocked evaluation (task_auto_merge.reason).
const (
	ReasonPaused            = "paused"
	ReasonAlwaysAsk         = "always_ask"
	ReasonActiveRun         = "active_run"
	ReasonDraft             = "draft"
	ReasonConflict          = "conflict"
	ReasonMergeableUnknown  = "mergeable_unknown"
	ReasonChangesRequested  = "changes_requested"
	ReasonChecksFailed      = "checks_failed"
	ReasonChecksPending     = "checks_pending"
	ReasonNoChecks          = "no_checks"
	ReasonHeadMoved         = "head_moved"
	ReasonMergeRefused      = "merge_refused"
	noChecksGrace           = 3 * time.Minute
	liveRunStatusPending    = "pending"
	liveRunStatusRunning    = "running"
	liveRunStatusWaitingHum = "waiting_human"
)

// Decision is the outcome of evaluating one task's PR.
type Decision struct {
	// Applies is false when auto-merge is not relevant to the task at all
	// (mode off, archived, no open PR); nothing is recorded for it.
	Applies bool
	// Eligible means every condition holds and the PR may be merged now.
	Eligible bool
	// Reason is a Reason* code when Applies && !Eligible.
	Reason string
	// Message is a short human-readable explanation of Reason.
	Message string
}

func blocked(reason, msg string) Decision {
	return Decision{Applies: true, Reason: reason, Message: msg}
}

// Input is everything Evaluate needs about a task's PR.
type Input struct {
	Task gen.Task
	Repo gen.Repo
	// HasLiveRun is true when the task has a pending/running/waiting run.
	HasLiveRun bool
	PR         forge.PRHead
	// Checks are all checks on the PR head commit; Reviews all PR reviews.
	Checks  []forge.Check
	Reviews []forge.Review
	// NoChecksSettled is true once the head commit has reported zero checks for
	// longer than the grace period, i.e. the repo evidently has no CI.
	NoChecksSettled bool
}

// Mode returns the repo's effective mode (empty reads as off).
func Mode(r gen.Repo) string {
	if r.AutoMergeMode == ModeWhenGreen {
		return ModeWhenGreen
	}
	return ModeOff
}

// Method returns the repo's effective merge method (empty reads as squash).
func Method(r gen.Repo) forge.MergeMethod {
	m := forge.MergeMethod(r.AutoMergeMethod)
	if forge.ValidMergeMethod(m) {
		return m
	}
	return forge.MergeSquash
}

// Precheck evaluates every condition that needs no extra forge calls. A
// decision with Eligible == false is final; Eligible == true only means the
// remaining (checks, reviews) conditions still have to be evaluated.
func Precheck(in Input) Decision {
	if Mode(in.Repo) != ModeWhenGreen || in.Task.Archived != 0 || in.Task.Branch == "" || in.PR.Number == 0 || in.PR.State != "pr_open" {
		return Decision{}
	}
	switch {
	case in.Task.Paused != 0:
		return blocked(ReasonPaused, "task is paused")
	case in.Task.AlwaysAsk != 0:
		return blocked(ReasonAlwaysAsk, "task is marked always ask")
	case in.HasLiveRun:
		return blocked(ReasonActiveRun, "an agent run is still active")
	case in.PR.Draft:
		return blocked(ReasonDraft, "PR is a draft")
	case in.PR.Mergeable == forge.MergeableConflicting:
		return blocked(ReasonConflict, "PR has merge conflicts")
	case in.PR.Mergeable != forge.MergeableClean:
		return blocked(ReasonMergeableUnknown, "forge has not computed mergeability yet")
	}
	return Decision{Applies: true, Eligible: true}
}

// Evaluate is the full eligibility decision: Precheck, then reviews, then checks.
func Evaluate(in Input) Decision {
	d := Precheck(in)
	if !d.Applies || !d.Eligible {
		return d
	}
	if changesRequested(in.Reviews) {
		return blocked(ReasonChangesRequested, "a reviewer requested changes")
	}
	var pending, failed int
	for _, c := range in.Checks {
		switch c.Bucket {
		case "pass", "skipping":
		case "fail", "cancel":
			failed++
		default: // "pending" and anything unrecognised: not proven green
			pending++
		}
	}
	switch {
	case failed > 0:
		return blocked(ReasonChecksFailed, fmt.Sprintf("%d check(s) failed", failed))
	case pending > 0:
		return blocked(ReasonChecksPending, fmt.Sprintf("%d check(s) still pending", pending))
	case len(in.Checks) == 0 && !in.NoChecksSettled:
		return blocked(ReasonNoChecks, "waiting to see whether any checks are reported")
	}
	return Decision{Applies: true, Eligible: true}
}

// changesRequested reports whether, for any reviewer, the latest decisive
// review (approval, change request or dismissal) is a change request.
func changesRequested(reviews []forge.Review) bool {
	sorted := append([]forge.Review(nil), reviews...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].SubmittedAt < sorted[j].SubmittedAt })
	latest := map[string]string{}
	for _, r := range sorted {
		switch strings.ToUpper(r.State) {
		case "CHANGES_REQUESTED", "APPROVED", "DISMISSED":
			latest[r.Author] = strings.ToUpper(r.State)
		}
	}
	for _, st := range latest {
		if st == "CHANGES_REQUESTED" {
			return true
		}
	}
	return false
}

// Source supplies the forge facts Evaluate needs beyond forge.PRHead.
type Source interface {
	PRChecks(ctx context.Context, prNumber int) ([]forge.Check, error)
	PRReviews(ctx context.Context, prNumber int) ([]forge.Review, error)
}

// Assess gathers the inputs for a task's PR and evaluates them, making forge
// calls only when the local preconditions already pass.
func Assess(ctx context.Context, q *gen.Queries, src Source, task gen.Task, repo gen.Repo, head forge.PRHead) (Decision, error) {
	in := Input{Task: task, Repo: repo, PR: head}
	if task.CurrentAgentRunID != nil && *task.CurrentAgentRunID != "" {
		run, err := q.GetAgentRun(ctx, *task.CurrentAgentRunID)
		if err == nil {
			switch run.Status {
			case liveRunStatusPending, liveRunStatusRunning, liveRunStatusWaitingHum:
				in.HasLiveRun = true
			}
		} else if !errors.Is(err, sql.ErrNoRows) {
			return Decision{}, fmt.Errorf("get current run: %w", err)
		}
	}
	if d := Precheck(in); !d.Applies || !d.Eligible {
		return d, nil
	}
	var err error
	if in.Checks, err = src.PRChecks(ctx, head.Number); err != nil {
		return Decision{}, fmt.Errorf("list checks: %w", err)
	}
	if in.Reviews, err = src.PRReviews(ctx, head.Number); err != nil {
		return Decision{}, fmt.Errorf("list reviews: %w", err)
	}
	if len(in.Checks) == 0 {
		if row, err := q.GetTaskAutoMerge(ctx, task.ID); err == nil && row.Reason == ReasonNoChecks && row.HeadSha == head.HeadSHA &&
			time.Since(row.UpdatedAt) >= noChecksGrace {
			in.NoChecksSettled = true
		}
	}
	return Evaluate(in), nil
}

// Enqueuer is the part of workflow.Engine Reconcile uses.
type Enqueuer interface {
	EnqueueAutoMerge(ctx context.Context, taskID string) (bool, error)
}

// Publisher publishes WebSocket events.
type Publisher interface {
	Publish(eventType string, payload any)
}

// Reconcile assesses the task's PR, records the verdict for the UI and, when
// the PR is eligible, enqueues the merge through the outbox. Called by ghsync
// on every PR refresh. Returns the decision.
func Reconcile(ctx context.Context, q *gen.Queries, enq Enqueuer, pub Publisher, src Source, task gen.Task, repo gen.Repo, head forge.PRHead) (Decision, error) {
	d, err := Assess(ctx, q, src, task, repo, head)
	if err != nil {
		return d, err
	}
	prev, prevErr := q.GetTaskAutoMerge(ctx, task.ID)
	had := prevErr == nil
	if !d.Applies {
		if had {
			if _, err := q.DeleteTaskAutoMerge(ctx, task.ID); err != nil {
				return d, err
			}
			publishUpdated(pub, task.ID)
		}
		return d, nil
	}
	state, reason, msg := StateWaiting, d.Reason, d.Message
	if d.Eligible {
		state, reason, msg = StateMerging, "", ""
	}
	if err := q.UpsertTaskAutoMerge(ctx, gen.UpsertTaskAutoMergeParams{
		TaskID: task.ID, State: state, Reason: reason, Message: msg, HeadSha: head.HeadSHA,
	}); err != nil {
		return d, err
	}
	if !had || prev.State != state || prev.Reason != reason || prev.Message != msg {
		publishUpdated(pub, task.ID)
	}
	if d.Eligible && enq != nil {
		if _, err := enq.EnqueueAutoMerge(ctx, task.ID); err != nil {
			return d, fmt.Errorf("enqueue auto-merge: %w", err)
		}
	}
	return d, nil
}

func publishUpdated(pub Publisher, taskID string) {
	if pub != nil {
		pub.Publish(wsevent.TypeTaskUpdated, wsevent.TaskUpdated{ID: taskID})
	}
}

// ForgeSource adapts a forge.Forge + repo name to Source.
type ForgeSource struct {
	Forge    forge.Forge
	RepoName string
}

func (s ForgeSource) PRChecks(ctx context.Context, n int) ([]forge.Check, error) {
	return s.Forge.PRChecks(ctx, s.RepoName, n)
}

func (s ForgeSource) PRReviews(ctx context.Context, n int) ([]forge.Review, error) {
	return s.Forge.PRReviews(ctx, s.RepoName, n)
}

// Merge is the body of the auto_merge outbox effect. It re-reads live PR state,
// re-evaluates (a person may have paused the task or a check may have gone red
// since ghsync enqueued the merge) and merges pinned to the evaluated head SHA.
//
//   - Not eligible any more, or the head moved: no merge, nil; ghsync's next
//     refresh re-evaluates.
//   - The forge refused (branch protection, required review): a Permanent error
//     carrying the forge's explanation, so the task shows up in Needs you with
//     Retry/Dismiss instead of looping.
//   - Transient errors are returned as-is and retried with backoff.
//
// A successful merge is recorded in label history (actor auto-merge); the
// existing merged-PR sync then moves the task on and cleans up.
func Merge(ctx context.Context, q *gen.Queries, pub Publisher, f forge.Forge, repoName string, task gen.Task, repo gen.Repo) error {
	if Mode(repo) != ModeWhenGreen {
		return nil
	}
	log := slog.With("component", "automerge", "task_id", task.ID)
	head, err := f.PRHead(ctx, repoName, task.Branch)
	if err != nil {
		return fmt.Errorf("get PR head: %w", err)
	}
	if head.State != "pr_open" || head.Number == 0 {
		return nil
	}
	d, err := Assess(ctx, q, ForgeSource{Forge: f, RepoName: repoName}, task, repo, head)
	if err != nil {
		return err
	}
	if !d.Applies || !d.Eligible {
		log.Info("auto-merge skipped: no longer eligible", "reason", d.Reason)
		return nil
	}
	method := Method(repo)
	if err := f.MergePullRequest(ctx, repoName, head.Number, method, head.HeadSHA); err != nil {
		if errors.Is(err, forge.ErrHeadMoved) {
			log.Info("auto-merge skipped: head moved", "err", err)
			return nil
		}
		if errors.Is(err, forge.ErrMergeRefused) {
			_ = q.UpsertTaskAutoMerge(ctx, gen.UpsertTaskAutoMergeParams{
				TaskID: task.ID, State: StateWaiting, Reason: ReasonMergeRefused, Message: err.Error(), HeadSha: head.HeadSHA,
			})
			return workflow.Permanent(fmt.Errorf("auto-merge of PR #%d refused: %w", head.Number, err))
		}
		return err
	}
	log.Info("auto-merged PR", "pr", head.Number, "method", method)
	from := task.Label
	actor := workflow.ActorAutoMerge
	note := fmt.Sprintf("Auto-merged PR #%d (%s) after checks passed", head.Number, method)
	if err := q.CreateTaskLabelHistory(ctx, gen.CreateTaskLabelHistoryParams{
		ID: uuid.NewString(), TaskID: task.ID, FromLabel: &from, ToLabel: task.Label,
		Trigger: string(workflow.TriggerHuman), ActorID: &actor, Note: &note,
	}); err != nil {
		// The PR is merged; failing here would retry a merge that can't happen.
		log.Warn("auto-merge: record history", "err", err)
	}
	if _, err := q.DeleteTaskAutoMerge(ctx, task.ID); err != nil {
		log.Warn("auto-merge: clear state", "err", err)
	}
	publishUpdated(pub, task.ID)
	return nil
}
