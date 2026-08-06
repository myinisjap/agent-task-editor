package tasksource

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/myinisjap/agent-task-editor/backend/internal/intake"
	"github.com/myinisjap/agent-task-editor/backend/internal/metrics"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
	"github.com/myinisjap/agent-task-editor/backend/internal/workflow"
)

// Publisher is satisfied by *ws.Hub — it sends events to all connected clients.
type Publisher interface {
	Publish(eventType string, payload map[string]any)
}

// Importer polls all repos with issue sync enabled and keeps their imported
// tasks in sync with the external item they came from:
//
//   - New items (never imported before) are created as new tasks, deduped by
//     (source, source_ref): as long as the imported task exists (in any
//     state), the item is never imported again. Deleting an imported task
//     while the external item still matches the filter causes it to be
//     re-imported on the next sweep — to stop that, remove the filter label
//     from the issue instead.
//   - Existing tasks have their title/description/type drift applied from the
//     upstream item, subject to the repo's issue_sync_update_policy (never
//     touching label, archived, or any writeback_* flag — see
//     UpdateTaskFromSource).
//   - The item's human comment thread is optionally ingested (opt-in per repo,
//     following the same update-policy gate) when the Source also implements
//     CommentSource.
//   - Items that disappear from a later fetch (closed, or no longer matching
//     the filter) are reconciled: source_state is flagged "gone", and the
//     repo's issue_sync_gone_action optionally archives or moves the task.
//     Reconciliation never deletes a task or un-imports it — it only sets
//     state and optionally archives/moves.
type Importer struct {
	db       *sql.DB
	q        *gen.Queries
	hub      Publisher
	interval time.Duration
	// sources holds every configured Source, tried in order for each repo
	// (see resolveSource). Almost always populated with exactly one element
	// (via New/NewWithEngine); NewMulti/NewWithEngineMulti populate more than
	// one so a single Importer can sync repos hosted on different forges
	// (e.g. GitHubIssues for github.com remotes, GiteaIssues for a
	// self-hosted Gitea) without running a whole separate poller per forge.
	sources []Source
	// engine, if non-nil, is used to execute the "move" gone-action. Nil-safe:
	// a nil engine (or an empty issue_sync_gone_label, or a rejected
	// transition) falls back to flag-only behavior.
	engine *workflow.Engine
}

// New creates an Importer that polls the given source on the given interval.
// The returned Importer has no workflow engine wired in, so a repo configured
// with issue_sync_gone_action = "move" falls back to flag-only for that repo
// (see NewWithEngine).
func New(db *sql.DB, hub Publisher, interval time.Duration, source Source) *Importer {
	return NewMulti(db, hub, interval, []Source{source})
}

// NewMulti creates an Importer that tries each of the given sources, in
// order, for every repo it sweeps — see resolveSource. Use this (rather than
// New) when more than one external-tracker Source is configured (e.g.
// GitHub Issues and Gitea Issues side by side).
func NewMulti(db *sql.DB, hub Publisher, interval time.Duration, sources []Source) *Importer {
	return &Importer{
		db:       db,
		q:        gen.New(db),
		hub:      hub,
		interval: interval,
		sources:  sources,
	}
}

// NewWithEngine creates an Importer that also executes the "move" gone-action
// via the given workflow engine. Kept as a separate constructor (rather than
// changing New's signature) so existing callers/tests that construct an
// Importer without an engine keep compiling unchanged.
func NewWithEngine(db *sql.DB, hub Publisher, interval time.Duration, source Source, engine *workflow.Engine) *Importer {
	im := New(db, hub, interval, source)
	im.engine = engine
	return im
}

// NewWithEngineMulti is NewMulti plus a workflow engine — see NewWithEngine
// and NewMulti.
func NewWithEngineMulti(db *sql.DB, hub Publisher, interval time.Duration, sources []Source, engine *workflow.Engine) *Importer {
	im := NewMulti(db, hub, interval, sources)
	im.engine = engine
	return im
}

// Run sweeps on the configured interval until ctx is cancelled.
func (im *Importer) Run(ctx context.Context) {
	ticker := time.NewTicker(im.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			im.Sweep(ctx)
		}
	}
}

// sweepStats tallies what one repo's sweep did, for summary logging.
type sweepStats struct {
	created   int
	updated   int
	commented int
	flagged   int // items reconciled as "gone" this sweep (any gone action)
}

// maxCreatedIDsInEvent caps how many task ids are inlined in a single
// task.created_bulk event's payload. A sweep that creates far more than this
// (e.g. first import of a large backlog) would otherwise risk a WS payload
// large enough to itself contend with the hub's bounded per-client send
// buffer (see ws.Hub.Publish) — count is always exact; ids beyond the cap are
// simply omitted and clients fall back to their normal task list refresh.
const maxCreatedIDsInEvent = 500

// Sweep syncs every issue-sync-enabled repo: creating tasks for new items,
// updating existing ones, ingesting comments, and reconciling disappeared
// items.
func (im *Importer) Sweep(ctx context.Context) {
	start := time.Now()
	defer func() { metrics.TasksourceSweepDurationSeconds.Observe(time.Since(start).Seconds()) }()

	log := slog.With("component", "tasksource")
	repos, err := im.q.ListIssueSyncRepos(ctx)
	if err != nil {
		log.Warn("issue import: list repos failed", "err", err)
		return
	}
	if len(repos) == 0 {
		return
	}
	log.Info("issue import: sweep start", "repos", len(repos))

	// Rules are loaded once per sweep (not once per repo): the candidate set
	// is the same for every repo, and ListEnabledIntakeRules is already
	// ordered for first-match-wins evaluation — see internal/intake.
	rules, err := im.q.ListEnabledIntakeRules(ctx)
	if err != nil {
		log.Warn("issue import: list intake rules failed; importing without rule shaping", "err", err)
		rules = nil
	}

	var total sweepStats
	warnedDeprecated := false
	for _, repo := range repos {
		// issue_sync_label is deprecated in favor of intake rules (see
		// docs/task-sources.md): it still narrows the fetch, but authors
		// should migrate to a rule with match_labels for shaping. Warn at
		// most once per sweep, not once per repo, to avoid log spam on a
		// large fleet of repos that haven't migrated yet.
		if repo.IssueSyncLabel != "" && !warnedDeprecated {
			log.Warn("issue import: issue_sync_label is deprecated; configure an equivalent intake rule instead (see docs/task-sources.md)")
			warnedDeprecated = true
		}
		s := im.sweepRepo(ctx, repo, rules)
		total.created += s.created
		total.updated += s.updated
		total.commented += s.commented
		total.flagged += s.flagged
	}
	log.Info("issue import: sweep done",
		"created", total.created,
		"updated", total.updated,
		"comments_ingested", total.commented,
		"flagged_gone", total.flagged,
	)
}

// resolveSource picks the Source able to handle repo out of im.sources: the
// first one whose Fetch call doesn't immediately fail for lacking a usable
// remote URL. Sources are expected to validate the remote (e.g. via
// forge.ForRemote) before making any network call, so trying each in order
// here costs nothing beyond a cheap string comparison per candidate — no
// wasted API calls against the wrong forge.
//
// Returns ok=false if repo has no remote URL a configured source recognises
// (e.g. a Gitea remote when only GitHubIssues is configured, or vice versa).
func (im *Importer) resolveSource(repo gen.Repo) (Source, bool) {
	if repo.RemoteUrl == nil || *repo.RemoteUrl == "" {
		return nil, false
	}
	for _, s := range im.sources {
		if r, ok := s.(interface{ AppliesTo(gen.Repo) bool }); ok {
			if r.AppliesTo(repo) {
				return s, true
			}
			continue
		}
		// A Source without an AppliesTo capability is assumed to always
		// apply (kept for any hypothetical future Source that isn't
		// per-remote-forge-scoped); with exactly one such Source configured
		// (the common case), this is exactly today's single-source behavior.
		return s, true
	}
	return nil, false
}

// sweepRepo syncs tasks for one repo: fetch -> per-item create-or-update ->
// reconcile disappeared items. rules is the enabled intake-rule set for this
// sweep (see Sweep), already ordered for first-match-wins evaluation; nil is
// a valid value (rule lookup failed or there are no rules) and simply means
// every item falls back to the gate label with no shaping.
func (im *Importer) sweepRepo(ctx context.Context, repo gen.Repo, rules []gen.IntakeRule) sweepStats {
	log := slog.With("component", "tasksource", "repo", repo.Name)
	var stats sweepStats

	source, ok := im.resolveSource(repo)
	if !ok {
		log.Warn("issue import: no configured source recognises this repo's remote; skipping")
		return stats
	}

	// Tasks require a workflow; a repo without one can't receive imports.
	if repo.WorkflowID == nil || *repo.WorkflowID == "" {
		log.Warn("issue import: repo has issue sync enabled but no workflow assigned; skipping")
		return stats
	}

	// Imported tasks land on the workflow's human-gate label (the lowest
	// sort_order agent_ignore label, falling back to the first label) so a human
	// promotes them before an agent picks them up — "not_ready" for the default
	// workflow, the equivalent gate for any custom one. The gate label also
	// determines when field updates / comment ingestion are allowed under the
	// default "gate" update policy.
	//
	// labelCache memoizes ListWorkflowLabels per workflow id for the rest of
	// this sweepRepo call: a rule may override the effective workflow via
	// apply_workflow_id (see resolveRuleTarget), so more than one workflow's
	// labels can be looked up in a single sweep of one repo.
	labelCache := map[string][]gen.WorkflowLabel{}
	labels, err := im.workflowLabels(ctx, *repo.WorkflowID, labelCache)
	if err != nil {
		log.Warn("issue import: label lookup failed", "workflow_id", *repo.WorkflowID, "err", err)
		return stats
	}
	gate, first := workflow.GateLabel(labels)
	startLabel := gate
	if startLabel == "" {
		startLabel = first
	}
	if startLabel == "" {
		log.Warn("issue import: repo workflow has no labels; skipping")
		return stats
	}

	items, err := source.Fetch(ctx, repo)
	if err != nil {
		log.Warn("issue import: fetch failed", "err", err)
		return stats
	}

	commenter, canComment := source.(CommentSource)

	// New items are created in a single DB transaction for the whole repo,
	// rather than one implicit commit per row: SQLite is single-writer, and a
	// large backlog importing one row per commit would repeatedly acquire and
	// release the write lock, blocking every other writer (agent pool, API)
	// on busy_timeout for the duration. See createNewTasks.
	var toCreate []ExternalTask

	seen := make(map[string]bool, len(items))
	for _, item := range items {
		seen[item.Ref] = true

		task, err := im.q.GetTaskBySource(ctx, gen.GetTaskBySourceParams{
			Source:    source.Name(),
			SourceRef: item.Ref,
		})
		if errors.Is(err, sql.ErrNoRows) {
			toCreate = append(toCreate, item)
			continue
		}
		if err != nil {
			log.Warn("issue import: lookup failed", "ref", item.Ref, "err", err)
			continue
		}

		// The issue reopened / regained the filter label since it was last
		// flagged gone: clear the flag before anything else.
		if task.SourceState == "gone" {
			if err := im.q.SetTaskSourceState(ctx, gen.SetTaskSourceStateParams{SourceState: "", ID: task.ID}); err != nil {
				log.Warn("issue import: clear gone state failed", "task_id", task.ID, "err", err)
			} else {
				task.SourceState = ""
				im.hub.Publish("task.updated", map[string]any{"id": task.ID})
			}
		}

		if !updateAllowed(repo, task.Label, startLabel) {
			continue
		}

		if im.applyFieldUpdates(ctx, &task, item, rules, log) {
			stats.updated++
		}

		if repo.IssueCommentSyncEnabled != 0 && canComment {
			stats.commented += im.ingestComments(ctx, commenter, repo, task, item.Ref, log)
		}
	}

	stats.created += im.createNewTasks(ctx, repo, source.Name(), startLabel, toCreate, rules, labelCache, log)

	stats.flagged += im.reconcile(ctx, repo, source.Name(), seen, log)
	return stats
}

// workflowLabels fetches (and memoizes in cache) the workflow_labels for
// workflowID, so a sweep that touches more than one effective workflow
// (repo default plus any apply_workflow_id overrides from matched rules)
// only looks each one up once.
func (im *Importer) workflowLabels(ctx context.Context, workflowID string, cache map[string][]gen.WorkflowLabel) ([]gen.WorkflowLabel, error) {
	if labels, ok := cache[workflowID]; ok {
		return labels, nil
	}
	labels, err := im.q.ListWorkflowLabels(ctx, workflowID)
	if err != nil {
		return nil, err
	}
	cache[workflowID] = labels
	return labels, nil
}

// createNewTasks imports every not-yet-seen external item for one repo in a
// single DB transaction (one commit, one write-lock acquisition, instead of
// one per item) and — if at least one task was created — emits a single
// task.created_bulk event instead of one task.created per item. The event is
// only published after the transaction commits successfully, so it always
// reflects DB truth.
//
// rules is the enabled intake-rule set for this sweep (nil if none/lookup
// failed); labelCache memoizes ListWorkflowLabels per workflow id for rules
// that override the effective workflow via apply_workflow_id (see
// shapeItem).
func (im *Importer) createNewTasks(ctx context.Context, repo gen.Repo, sourceName, startLabel string, items []ExternalTask, rules []gen.IntakeRule, labelCache map[string][]gen.WorkflowLabel, log *slog.Logger) int {
	if len(items) == 0 {
		return 0
	}

	tx, err := im.db.BeginTx(ctx, nil)
	if err != nil {
		log.Warn("issue import: begin create transaction failed", "err", err)
		return 0
	}
	defer tx.Rollback() //nolint:errcheck // no-op after a successful Commit

	qtx := im.q.WithTx(tx)

	created := make([]gen.Task, 0, len(items))
	for _, item := range items {
		shaped := im.shapeItem(ctx, repo, item, startLabel, rules, labelCache, log)

		task, err := qtx.CreateSourcedTask(ctx, gen.CreateSourcedTaskParams{
			ID:            uuid.NewString(),
			Title:         shaped.title,
			Description:   shaped.description,
			Type:          shaped.taskType,
			Label:         shaped.label,
			RepoID:        repo.ID,
			WorkflowID:    shaped.workflowID,
			Attachments:   "[]",
			Source:        sourceName,
			SourceRef:     item.Ref,
			Priority:      shaped.priority,
			MaxCostUsd:    shaped.maxCostUsd,
			MatchedRuleID: shaped.matchedRuleID,
		})
		if err != nil {
			// A UNIQUE violation here means a concurrent insert won the race —
			// harmless; anything else is worth surfacing. Either way, a failed
			// INSERT only rolls back that statement in SQLite, not the whole
			// transaction, so the rest of the batch still commits.
			log.Warn("issue import: create task failed", "ref", item.Ref, "err", err)
			continue
		}
		created = append(created, task)
	}

	if len(created) == 0 {
		return 0
	}

	if err := tx.Commit(); err != nil {
		log.Warn("issue import: commit create transaction failed", "count", len(created), "err", err)
		return 0
	}

	for _, task := range created {
		log.Info("issue import: task created", "task_id", task.ID, "ref", task.SourceRef)
	}

	ids := make([]string, 0, len(created))
	for i, task := range created {
		if i >= maxCreatedIDsInEvent {
			break
		}
		ids = append(ids, task.ID)
	}
	im.hub.Publish("task.created_bulk", map[string]any{
		"repo_id": repo.ID,
		"source":  sourceName,
		"count":   len(created),
		"ids":     ids,
	})

	return len(created)
}

// composeDescription builds a task's description from an external item's body
// and web link, exactly the same way for both the create and update paths so
// re-sweeping an unchanged issue never sees spurious drift.
func composeDescription(body, url string) string {
	description := body
	if url != "" {
		if description != "" {
			description += "\n\n"
		}
		description += "_Imported from " + url + "_"
	}
	return description
}

// shapedItem is the result of applying rule matching + (optional) template
// shaping to one external item — the fields both createNewTasks and
// applyFieldUpdates need to agree on, so a task is never "corrected" back
// and forth between what the create path wrote and what the update path
// would compute (see the drift-thrash note on applyFieldUpdates).
type shapedItem struct {
	title         string
	description   string
	taskType      string
	label         string
	workflowID    string
	priority      int64
	maxCostUsd    float64
	matchedRuleID *string
}

// shapeItem computes the full set of task fields for one external item:
// matches it against rules, resolves the effective workflow/label (applying
// the security gate before ever letting a rule bypass the human-review
// gate), and layers an optional template's type/description-prefix on top.
//
// Field precedence (deliberate, documented here since it's otherwise
// unguessable):
//   - title: always the issue's own title (an imported task must stay
//     identifiable against its source issue; a template never overrides it).
//   - type: the template's type if a rule applied one, else the
//     TaskTypeFromLabels heuristic on the issue's own labels.
//   - description: the composed issue body+link, with the template's own
//     description (if any) prepended as context.
//   - label: the rule's apply_target_label if set AND permitted by
//     intake.AutoStartAllowed AND it is a real label in the *effective*
//     workflow; otherwise startLabel (the gate). A rule that fails the
//     auto-start gate or names an unrecognised label falls back to the gate
//     rather than being silently dropped — imported issue content is
//     untrusted, so "fail closed to the human gate" is the only safe
//     default here (see #331).
//   - workflow: the rule's apply_workflow_id if set, else the repo's own
//     workflow. When overridden, the gate/label validity check above is
//     re-evaluated against *that* workflow's labels, not the repo's.
func (im *Importer) shapeItem(ctx context.Context, repo gen.Repo, item ExternalTask, startLabel string, rules []gen.IntakeRule, labelCache map[string][]gen.WorkflowLabel, log *slog.Logger) shapedItem {
	shaped := shapedItem{
		title:       item.Title,
		description: composeDescription(item.Body, item.URL),
		taskType:    TaskTypeFromLabels(item.Labels),
		label:       startLabel,
		workflowID:  *repo.WorkflowID,
	}

	decision, rule, ok := matchRule(rules, intake.Candidate{
		Source:      "issue",
		RepoID:      repo.ID,
		Title:       item.Title,
		Body:        item.Body,
		Labels:      item.Labels,
		AuthorAssoc: item.AuthorAssoc,
	})
	if !ok {
		return shaped
	}

	// Resolve the effective workflow first: a target-label validity/gate
	// check must run against the workflow the task will actually land in.
	effectiveWorkflowID := shaped.workflowID
	if decision.WorkflowID != nil && *decision.WorkflowID != "" {
		effectiveWorkflowID = *decision.WorkflowID
	}
	effectiveLabels, err := im.workflowLabels(ctx, effectiveWorkflowID, labelCache)
	if err != nil {
		log.Warn("issue import: rule workflow label lookup failed; falling back to repo default", "rule_id", decision.RuleID, "workflow_id", effectiveWorkflowID, "err", err)
		effectiveWorkflowID = shaped.workflowID
		effectiveLabels, err = im.workflowLabels(ctx, effectiveWorkflowID, labelCache)
		if err != nil {
			log.Warn("issue import: repo workflow label lookup failed; using startLabel as-is", "err", err)
			effectiveLabels = nil
		}
	}
	gate, first := workflow.GateLabel(effectiveLabels)
	effectiveGate := gate
	if effectiveGate == "" {
		effectiveGate = first
	}

	shaped.workflowID = effectiveWorkflowID
	shaped.label = effectiveGate
	if shaped.label == "" {
		// The overridden workflow has no labels at all; fall back to the
		// caller's original (repo-default) gate rather than an empty label.
		shaped.label = startLabel
		shaped.workflowID = *repo.WorkflowID
	}

	if decision.TargetLabel != "" {
		targetIsGate := decision.TargetLabel == effectiveGate
		labelExists := false
		for _, l := range effectiveLabels {
			if l.Name == decision.TargetLabel {
				labelExists = true
				break
			}
		}
		if !labelExists {
			log.Warn("issue import: rule target_label is not a label in the effective workflow; falling back to gate", "rule_id", decision.RuleID, "target_label", decision.TargetLabel)
		} else if !intake.AutoStartAllowed(rule, targetIsGate) {
			// Defensive re-check: the CRUD handler should already have
			// rejected a rule combining an agent-triggerable target label
			// with no/untrusted author-association constraint, but a rule
			// could still reach here via a direct DB edit or a future code
			// path — never let imported issue content silently bypass the
			// human-review gate. See intake.AutoStartAllowed's doc comment.
			log.Warn("issue import: rule target_label bypasses the auto-start safety gate; falling back to gate label", "rule_id", decision.RuleID, "target_label", decision.TargetLabel)
		} else {
			shaped.label = decision.TargetLabel
		}
	}

	if decision.Priority != nil {
		shaped.priority = *decision.Priority
	}
	if decision.MaxCostUsd != nil {
		shaped.maxCostUsd = *decision.MaxCostUsd
	}
	ruleID := decision.RuleID
	shaped.matchedRuleID = &ruleID

	if decision.TemplateID != nil && *decision.TemplateID != "" {
		tmpl, err := im.q.GetTaskTemplate(ctx, *decision.TemplateID)
		if err != nil {
			log.Warn("issue import: rule template lookup failed; shaping without it", "rule_id", decision.RuleID, "template_id", *decision.TemplateID, "err", err)
		} else {
			shaped.taskType = tmpl.Type
			if tmpl.Description != "" {
				shaped.description = tmpl.Description + "\n\n" + shaped.description
			}
		}
	}

	return shaped
}

// matchRule runs intake.Match and, on a match, also returns the matched
// gen.IntakeRule itself (not just its Decision) — shapeItem needs the raw
// rule to re-run intake.AutoStartAllowed with the correctly-resolved
// "is this the gate label" bit, which Decision alone doesn't carry.
func matchRule(rules []gen.IntakeRule, c intake.Candidate) (intake.Decision, gen.IntakeRule, bool) {
	decision, ok := intake.Match(rules, c)
	if !ok {
		return intake.Decision{}, gen.IntakeRule{}, false
	}
	for _, r := range rules {
		if r.ID == decision.RuleID {
			return decision, r, true
		}
	}
	return decision, gen.IntakeRule{}, false
}

// updateAllowed implements the update-permission predicate shared by field
// updates and comment ingestion:
//   - "never" -> false
//   - "always" -> true
//   - "gate" (the documented default) and any unrecognized value (including
//     "" — see the Phase 1 carry-over note on repos.go writing zero values for
//     these columns) -> only while the task is still on the gate label.
func updateAllowed(repo gen.Repo, taskLabel, gateLabel string) bool {
	switch repo.IssueSyncUpdatePolicy {
	case "never":
		return false
	case "always":
		return true
	default:
		return taskLabel == gateLabel
	}
}

// applyFieldUpdates recomputes title/description/type exactly as the create
// path does — including any rule/template shaping (see shapeItem) — and
// writes drift to the task if anything differs. Returns true if a write
// happened. task is updated in place on success so later steps in the same
// sweep (e.g. comment ingestion, which doesn't currently need task fields
// but might) see the fresh row.
//
// Using shapeItem here (rather than only composeDescription/
// TaskTypeFromLabels, as this used to) is the fix for the update-thrash
// hazard: if this recomputed type/description WITHOUT the rule's template
// shaping applied at creation, a rule-shaped task would appear to have
// drifted on every subsequent sweep and get silently "corrected" back,
// producing an endless update loop and a task.updated WS event storm. Label
// is deliberately never touched here — matches the pre-existing contract of
// UpdateTaskFromSource never moving a task off wherever a human (or the
// create path) placed it.
func (im *Importer) applyFieldUpdates(ctx context.Context, task *gen.Task, item ExternalTask, rules []gen.IntakeRule, log *slog.Logger) bool {
	// startLabel/labelCache only affect the *label* shapeItem would choose,
	// which this call site ignores — passed through only because shapeItem
	// needs a repo to resolve template/workflow lookups against.
	repo, err := im.q.GetRepo(ctx, task.RepoID)
	if err != nil {
		log.Warn("issue import: repo lookup failed during update; skipping", "task_id", task.ID, "err", err)
		return false
	}
	labelCache := map[string][]gen.WorkflowLabel{}
	shaped := im.shapeItem(ctx, repo, item, task.Label, rules, labelCache, log)

	if task.Title == shaped.title && task.Description == shaped.description && task.Type == shaped.taskType {
		return false
	}

	updated, err := im.q.UpdateTaskFromSource(ctx, gen.UpdateTaskFromSourceParams{
		Title:       shaped.title,
		Description: shaped.description,
		Type:        shaped.taskType,
		ID:          task.ID,
	})
	if err != nil {
		log.Warn("issue import: update task failed", "task_id", task.ID, "ref", item.Ref, "err", err)
		return false
	}
	*task = updated
	log.Info("issue import: task updated from source", "task_id", task.ID, "ref", item.Ref)
	im.hub.Publish("task.updated", map[string]any{"id": task.ID})
	return true
}

// ingestComments fetches the item's comment thread and inserts any trusted,
// not-already-ingested, non-write-back comment. Returns the number of
// comments inserted. Best-effort: a fetch or insert failure is logged and
// skipped.
func (im *Importer) ingestComments(ctx context.Context, source CommentSource, repo gen.Repo, task gen.Task, ref string, log *slog.Logger) int {
	comments, err := source.FetchComments(ctx, repo, ref)
	if err != nil {
		log.Warn("issue import: fetch comments failed", "task_id", task.ID, "ref", ref, "err", err)
		return 0
	}

	inserted := 0
	for _, c := range comments {
		if !c.TrustedAuthor {
			continue
		}
		if _, err := im.q.GetTaskSourceCommentByExternalID(ctx, gen.GetTaskSourceCommentByExternalIDParams{
			TaskID:     task.ID,
			ExternalID: c.ID,
		}); err == nil {
			continue // already ingested
		}

		created, err := im.q.CreateTaskSourceComment(ctx, gen.CreateTaskSourceCommentParams{
			ID:                uuid.NewString(),
			TaskID:            task.ID,
			ExternalID:        c.ID,
			Author:            c.Author,
			Body:              c.Body,
			ExternalCreatedAt: c.CreatedAt,
		})
		if err != nil {
			log.Warn("issue import: create source comment failed", "task_id", task.ID, "external_id", c.ID, "err", err)
			continue
		}
		inserted++
		im.hub.Publish("task.source_comment_added", map[string]any{
			"task_id":    task.ID,
			"comment_id": created.ID,
		})
	}
	if inserted > 0 {
		log.Info("issue import: comments ingested", "task_id", task.ID, "count", inserted)
	}
	return inserted
}

// reconcile flags every previously-imported task for this repo whose item did
// not appear in this sweep's fetch (seen), and applies the repo's configured
// gone-action. Tasks already flagged gone are left untouched (idempotent).
// Never deletes a task or un-imports it — only sets state and optionally
// archives/moves.
func (im *Importer) reconcile(ctx context.Context, repo gen.Repo, sourceName string, seen map[string]bool, log *slog.Logger) int {
	existing, err := im.q.ListTasksBySourceRepo(ctx, gen.ListTasksBySourceRepoParams{
		Source: sourceName,
		RepoID: repo.ID,
	})
	if err != nil {
		log.Warn("issue import: list tasks for reconciliation failed", "err", err)
		return 0
	}

	flagged := 0
	for _, task := range existing {
		if seen[task.SourceRef] {
			continue
		}
		if task.SourceState == "gone" {
			continue // already handled, idempotent
		}
		if im.applyGoneAction(ctx, repo, task, log) {
			flagged++
		}
	}
	return flagged
}

// applyGoneAction records source_state = "gone" for a task whose source item
// disappeared from the fetch, then optionally escalates per
// repo.IssueSyncGoneAction. A task with an active agent run is only ever
// flagged, never archived or moved, even when the repo asks for
// archive/move — an agent may be mid-run; it is re-evaluated on the next
// sweep once the run clears. Reports whether the task was actually flagged, so
// a failed state write isn't counted in the sweep summary.
func (im *Importer) applyGoneAction(ctx context.Context, repo gen.Repo, task gen.Task, log *slog.Logger) bool {
	if err := im.q.SetTaskSourceState(ctx, gen.SetTaskSourceStateParams{SourceState: "gone", ID: task.ID}); err != nil {
		log.Warn("issue import: set gone state failed", "task_id", task.ID, "ref", task.SourceRef, "err", err)
		return false
	}
	im.hub.Publish("task.updated", map[string]any{"id": task.ID})
	log.Info("issue import: task flagged gone", "task_id", task.ID, "ref", task.SourceRef)

	activeRun := task.ActiveAgentRunID != nil && *task.ActiveAgentRunID != ""

	// The task is flagged either way from here on; an escalation that doesn't
	// apply (or fails) leaves it flagged rather than unflagged.
	switch repo.IssueSyncGoneAction {
	case "archive":
		if activeRun {
			log.Info("issue import: gone task has an active agent run; flagging only, not archiving", "task_id", task.ID)
			return true
		}
		if _, err := im.q.SetTaskArchived(ctx, gen.SetTaskArchivedParams{Archived: 1, ID: task.ID}); err != nil {
			log.Warn("issue import: archive gone task failed", "task_id", task.ID, "err", err)
			return true
		}
		log.Info("issue import: archived gone task", "task_id", task.ID)
	case "move":
		if activeRun {
			log.Info("issue import: gone task has an active agent run; flagging only, not moving", "task_id", task.ID)
			return true
		}
		if im.engine == nil || repo.IssueSyncGoneLabel == "" {
			return true
		}
		if err := im.engine.Transition(ctx, task.ID, repo.IssueSyncGoneLabel, workflow.TriggerHuman, "", "source issue closed or no longer matches the sync filter"); err != nil {
			log.Warn("issue import: move gone task failed", "task_id", task.ID, "to_label", repo.IssueSyncGoneLabel, "err", err)
			return true
		}
		log.Info("issue import: moved gone task", "task_id", task.ID, "to_label", repo.IssueSyncGoneLabel)
	default:
		// "flag" (the documented default) and any unrecognized value: stop here.
	}
	return true
}

// TaskTypeFromLabels maps external tracker labels to a board task type
// (feature | bug | chore | spike). First match wins; default is "feature".
func TaskTypeFromLabels(labels []string) string {
	for _, l := range labels {
		switch strings.ToLower(l) {
		case "bug", "defect", "regression":
			return "bug"
		case "chore", "maintenance", "dependencies", "ci", "refactor", "cleanup":
			return "chore"
		case "spike", "research", "question", "investigation":
			return "spike"
		case "enhancement", "feature", "feature-request":
			return "feature"
		}
	}
	return "feature"
}
