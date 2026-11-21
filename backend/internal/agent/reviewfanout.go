package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"path"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
)

// Review fan-out: for a large diff, an opted-in agent config's review run is
// split into focused reviewer passes (correctness, security, tests,
// conventions) that run inside ONE agent run. Each pass can only submit
// findings; a deterministic aggregation step (aggregateReviewFindings) drops
// low-confidence items, dedupes, and makes the single pass/fail decision. The
// run still ends as one ordinary Result, so persistence, verification,
// transitions and feedback routing are unchanged.

const (
	maxReviewSummaryFindings = 50
	maxReviewSummaryBytes    = 20 * 1024
	// reviewDedupeLineWindow is how far apart two findings in one file may be
	// and still count as the same finding.
	reviewDedupeLineWindow = 3
)

// reviewPasses are the fixed focused passes. Diff/DiffStat are filled per run.
var reviewPasses = []ReviewPassSpec{
	{
		Name:  "correctness",
		Title: "Correctness and logic bugs",
		Checklist: "- Logic errors, off-by-one mistakes, wrong conditions, unhandled edge cases\n" +
			"- Missing nil/error handling; errors swallowed or misreported\n" +
			"- Concurrency problems: races, deadlocks, leaked goroutines, shared mutable state\n" +
			"- Broken contracts: changed behaviour that existing callers, APIs or stored data still depend on",
	},
	{
		Name:  "security",
		Title: "Security",
		Checklist: "- Injection: SQL, shell/command, path traversal, template/HTML\n" +
			"- Authentication/authorization gaps and over-broad token scope\n" +
			"- Secrets or sensitive data written to logs, responses or the repo\n" +
			"- Untrusted input used without validation; unsafe file permissions; SSRF",
	},
	{
		Name:  "tests",
		Title: "Tests and coverage",
		Checklist: "- Changed or new behaviour without tests\n" +
			"- Weak assertions that cannot fail, or tests that assert implementation details\n" +
			"- Missing edge-case and failure-path tests\n" +
			"- Flaky patterns: sleeps, tight timeouts, order dependence, shared global state",
	},
	{
		Name:  "conventions",
		Title: "Project conventions and maintainability",
		Checklist: "- Rules in the repo's AGENTS.md / CLAUDE.md / ADRs that the change violates\n" +
			"- Naming, dead code, duplication, needless complexity\n" +
			"- Docs, CHANGELOG and API specs not updated; generated files not regenerated\n" +
			"- Maintainability: surprising behaviour, missing comments on non-obvious code",
	},
}

// providerSupportsParallelReviewPasses reports whether concurrent Run calls for
// one run are safe for the provider. The claude/codex/qwen runners hold only
// configuration (each Run makes its own MCP temp dir and subprocess); opencode
// runs share one on-disk session store, so its passes run one after another.
func providerSupportsParallelReviewPasses(provider string) bool {
	switch provider {
	case "claude", "codex_cli", "qwen_code":
		return true
	}
	return false
}

// diffStats is the size of a branch's diff against its merge-base.
type diffStats struct {
	Files int
	Lines int // added + deleted, text files only
}

// diffNumstatAgainstBase measures `git diff --numstat` of branch against its
// merge-base with baseRef. Binary files count as a file with 0 lines.
func diffNumstatAgainstBase(ctx context.Context, wtPath, baseRef, branch string) (diffStats, error) {
	var st diffStats
	if wtPath == "" || baseRef == "" || branch == "" {
		return st, errors.New("repo path, base ref and branch are required")
	}
	from := baseRef
	if mb, err := git(ctx, wtPath, "merge-base", baseRef, branch); err == nil {
		from = strings.TrimSpace(string(mb))
	}
	out, err := git(ctx, wtPath, "diff", "--numstat", from, branch, "--")
	if err != nil {
		return st, fmt.Errorf("git diff --numstat: %w: %s", err, strings.TrimSpace(string(out)))
	}
	for _, line := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		f := strings.SplitN(line, "\t", 3)
		if len(f) < 3 {
			continue
		}
		st.Files++
		if a, err := strconv.Atoi(f[0]); err == nil {
			st.Lines += a
		}
		if d, err := strconv.Atoi(f[1]); err == nil {
			st.Lines += d
		}
	}
	return st, nil
}

// reviewFanoutEligible is the cheap, git-free part of the fan-out decision.
// A human talking to a reviewer (reply, steering) or a PR review keeps the
// single session.
func reviewFanoutEligible(in RunInput) bool {
	return in.AgentConfig.ReviewFanoutEnabled &&
		in.ReviewPass == nil && in.PRReview == nil && in.HumanReply == nil && in.SteeringNote == nil
}

// shouldFanOutReview decides whether a run fans out given its diff size. A
// threshold of 0 disables that criterion; both 0 means always fan out.
func shouldFanOutReview(in RunInput, stats diffStats) bool {
	if !reviewFanoutEligible(in) {
		return false
	}
	minLines, minFiles := in.AgentConfig.ReviewFanoutMinLines, in.AgentConfig.ReviewFanoutMinFiles
	if minLines <= 0 && minFiles <= 0 {
		return true
	}
	return (minLines > 0 && int64(stats.Lines) >= minLines) || (minFiles > 0 && int64(stats.Files) >= minFiles)
}

// executeReview runs the provider for a job, fanning out into focused reviewer
// passes when the config opted in and the diff is large enough. With fan-out
// off it is exactly executeProvider (no git calls).
func (p *Pool) executeReview(ctx context.Context, job Job, log *slog.Logger) (Result, error) {
	if !reviewFanoutEligible(job.Input) || job.Input.RepoPath == "" {
		return p.executeProvider(ctx, job)
	}
	task, err := p.q.GetTask(ctx, job.Input.Task.ID)
	if err != nil || task.BaseRef == "" || task.Branch == "" {
		log.Warn("pool: review fan-out: cannot resolve task branch; using single reviewer", "err", err)
		return p.executeProvider(ctx, job)
	}
	stats, err := diffNumstatAgainstBase(ctx, job.Input.RepoPath, task.BaseRef, task.Branch)
	if err != nil {
		log.Warn("pool: review fan-out: diff size failed; using single reviewer", "err", err)
		return p.executeProvider(ctx, job)
	}
	if !shouldFanOutReview(job.Input, stats) {
		return p.executeProvider(ctx, job)
	}
	diff, err := diffAgainstBase(ctx, job.Input.RepoPath, task.BaseRef, task.Branch)
	if err != nil {
		log.Warn("pool: review fan-out: diff failed; using single reviewer", "err", err)
		return p.executeProvider(ctx, job)
	}
	if len(diff) > maxPRDiffBytes {
		diff = diff[:maxPRDiffBytes] + "\n[diff truncated: run `git diff $(git merge-base " + task.BaseRef + " " + task.Branch + ") " + task.Branch + "` in the worktree for the rest]\n"
	}
	stat := diffStatAgainstBase(ctx, job.Input.RepoPath, task.BaseRef, task.Branch, 200)
	return p.executeReviewFanout(ctx, job, stats, diff, stat)
}

// executeReviewFanout runs the passes with a shared log drain, then appends the
// review summary to the task's agent notes (a single reviewer would, and
// persistRunResult replaces agent_notes with Result.Notes).
func (p *Pool) executeReviewFanout(ctx context.Context, job Job, stats diffStats, diff, stat string) (Result, error) {
	log := slog.With("component", "pool", "run_id", job.RunID, "task_id", job.Input.Task.ID)
	logCh := make(chan LogEntry, 256)
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer func() {
			if r := recover(); r != nil {
				log.Error("pool: persist logs panicked", "panic", r, "stack", string(debug.Stack()))
			}
		}()
		p.persistLogs(ctx, job.RunID, job.Input.Task.ID, logCh)
	}()

	logCh <- LogEntry{Type: LogSystem, At: time.Now(), Content: fmt.Sprintf(
		"review fan-out: %d changed lines / %d files met the threshold → %d focused passes", stats.Lines, stats.Files, len(reviewPasses))}
	result, err := runReviewPasses(ctx, job, diff, stat, logCh)
	close(logCh)
	<-done

	if err == nil && result.Message != nil {
		current := ""
		if t, gerr := p.q.GetTask(ctx, job.Input.Task.ID); gerr == nil {
			current = t.AgentNotes
		}
		notes := strings.TrimSpace(current + "\n\n## Automated review (fan-out)\n" + *result.Message)
		result.Notes = &notes
	}
	return result, err
}

// reviewPassOutcome is what one pass produced, input to the aggregation.
type reviewPassOutcome struct {
	Name      string
	Completed bool
	Err       string
	Result    *ReviewFindingsResult
	// Usage, for the stored breakdown.
	CostUSD float64
}

// runReviewPasses runs every pass (in parallel when the provider allows) and
// aggregates them into one ordinary Result.
func runReviewPasses(ctx context.Context, job Job, diff, stat string, logCh chan<- LogEntry) (Result, error) {
	n := len(reviewPasses)
	passCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	type passRun struct {
		res Result
		err error
	}
	runs := make([]passRun, n)

	var fwd sync.WaitGroup
	runOne := func(i int) {
		spec := reviewPasses[i]
		spec.Diff, spec.DiffStat = diff, stat
		in := reviewPassInput(job.Input, spec, n)

		ch := make(chan LogEntry, 64)
		fwd.Add(1)
		go func() {
			defer fwd.Done()
			for e := range ch {
				e.Content = "[review:" + spec.Name + "] " + e.Content
				logCh <- e
			}
		}()
		res, err := job.Provider.Run(passCtx, in, ch)
		close(ch)
		runs[i] = passRun{res, err}
		if err != nil && isRunLevelReviewErr(err) {
			cancel() // account/budget-level: no point finishing the siblings
		}
	}

	if providerSupportsParallelReviewPasses(job.Input.AgentConfig.Provider) {
		var wg sync.WaitGroup
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				defer func() {
					if r := recover(); r != nil {
						runs[i].err = fmt.Errorf("review pass panicked: %v", r)
					}
				}()
				runOne(i)
			}(i)
		}
		wg.Wait()
	} else {
		for i := 0; i < n; i++ {
			if passCtx.Err() != nil {
				runs[i].err = passCtx.Err()
				continue
			}
			runOne(i)
		}
	}
	fwd.Wait()

	var total Result
	var runLevel, genuine error
	outcomes := make([]reviewPassOutcome, n)
	for i, r := range runs {
		total.InputTokens += r.res.InputTokens
		total.OutputTokens += r.res.OutputTokens
		total.CostUSD += r.res.CostUSD
		total.CostUnknown = total.CostUnknown || r.res.CostUnknown
		total.CostWarned = total.CostWarned || r.res.CostWarned
		total.TurnsUsed += r.res.TurnsUsed
		oc := reviewPassOutcome{Name: reviewPasses[i].Name, CostUSD: r.res.CostUSD}
		switch {
		case r.err != nil:
			oc.Err = r.err.Error()
			if runLevel == nil && isRunLevelReviewErr(r.err) {
				runLevel = r.err
			}
			if genuine == nil {
				genuine = r.err
			}
		case r.res.ReviewFindings == nil:
			oc.Err = "pass finished without submitting findings"
		default:
			oc.Completed = true
			oc.Result = r.res.ReviewFindings
		}
		outcomes[i] = oc
	}

	// Cancelled by the caller (human stop / shutdown): surface the ctx error;
	// run() checks cancelledBy first.
	if ctx.Err() != nil {
		total.Status = "failed"
		return total, ctx.Err()
	}
	if runLevel != nil {
		total.Status = "failed"
		return total, runLevel
	}

	agg := aggregateReviewFindings(outcomes)
	if agg.CompletedPasses == 0 {
		total.Status = "failed"
		return total, errors.New("review fan-out: no pass produced findings")
	}
	msg := renderReviewSummary(agg)
	info := renderReviewBreakdown(agg)
	total.Status = "completed"
	total.Outcome = agg.Outcome
	total.Message = &msg
	total.StoredInfo = &info
	return total, nil
}

// isRunLevelReviewErr reports errors that concern the whole account or budget
// (retry/failover/escalate the entire run) rather than one pass.
func isRunLevelReviewErr(err error) bool {
	var rl *ErrRateLimit
	var ce *ErrCostBudgetExceeded
	var mt *ErrMaxTurns
	if errors.As(err, &mt) {
		return false
	}
	if errors.As(err, &rl) || errors.As(err, &ce) {
		return true
	}
	var te transientErr
	return errors.As(err, &te)
}

// reviewPassInput derives one pass's RunInput from the run's input.
func reviewPassInput(base RunInput, spec ReviewPassSpec, passes int) RunInput {
	in := base
	in.ReviewPass = &spec
	in.ResumeSessionID = ""
	in.ResumeSessionCostUSD = 0
	in.HumanReply = nil
	in.SteeringNote = nil
	in.OpenReviewComments = nil
	in.Transitions = nil
	in.AgentConfig.SubtasksEnabled = false
	in.AgentConfig.CrossRepoSubtasksEnabled = false
	if in.CostBudgetUSD > 0 && passes > 0 {
		// Split the remaining budget so parallel watchdogs can't jointly overshoot.
		in.CostBudgetUSD = in.CostSpentUSD + (in.CostBudgetUSD-in.CostSpentUSD)/float64(passes)
	}
	return in
}

// ---- aggregation ----

type aggFinding struct {
	ReviewFinding
	Passes []string
}

type reviewAggregate struct {
	Outcome         string // success | failure
	Findings        []aggFinding
	Dropped         int // low-confidence findings discarded
	Passes          []reviewPassOutcome
	CompletedPasses int
}

var severityRank = map[string]int{"blocker": 4, "major": 3, "minor": 2, "nit": 1}
var confidenceRank = map[string]int{"high": 3, "medium": 2, "low": 1}

func normalizeFinding(f ReviewFinding) (ReviewFinding, bool) {
	f.Title = strings.TrimSpace(f.Title)
	f.Body = strings.TrimSpace(f.Body)
	if f.Title == "" && f.Body == "" {
		return f, false
	}
	if f.Title == "" {
		f.Title = f.Body
		if i := strings.IndexAny(f.Title, ".\n"); i > 0 {
			f.Title = f.Title[:i]
		}
	}
	f.Severity = strings.ToLower(strings.TrimSpace(f.Severity))
	if _, ok := severityRank[f.Severity]; !ok {
		f.Severity = "minor"
	}
	f.Confidence = strings.ToLower(strings.TrimSpace(f.Confidence))
	if _, ok := confidenceRank[f.Confidence]; !ok {
		f.Confidence = "medium"
	}
	if p := strings.TrimSpace(f.Path); p != "" {
		p = path.Clean(p)
		if p == "." || path.IsAbs(p) || p == ".." || strings.HasPrefix(p, "../") {
			p = ""
			f.Line = 0
		}
		f.Path = p
	}
	if f.Line < 0 {
		f.Line = 0
	}
	return f, true
}

func titleWords(s string) map[string]bool {
	w := map[string]bool{}
	for _, t := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		w[t] = true
	}
	return w
}

func normTitle(s string) string {
	ws := strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	return strings.Join(ws, " ")
}

func jaccard(a, b map[string]bool) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	inter := 0
	for k := range a {
		if b[k] {
			inter++
		}
	}
	return float64(inter) / float64(len(a)+len(b)-inter)
}

func sameFinding(a, b ReviewFinding) bool {
	ta, tb := normTitle(a.Title), normTitle(b.Title)
	if a.Path == "" && b.Path == "" {
		return ta != "" && ta == tb
	}
	if a.Path == "" || b.Path == "" || a.Path != b.Path {
		return false
	}
	d := a.Line - b.Line
	if d < 0 {
		d = -d
	}
	if d > reviewDedupeLineWindow {
		return false
	}
	return ta == tb || jaccard(titleWords(a.Title), titleWords(b.Title)) >= 0.5
}

// aggregateReviewFindings drops low-confidence findings, dedupes across passes
// and decides the outcome: failure iff a kept finding is blocker or major.
func aggregateReviewFindings(passes []reviewPassOutcome) reviewAggregate {
	agg := reviewAggregate{Outcome: "success", Passes: passes}
	var kept []aggFinding
	for _, ps := range passes {
		if !ps.Completed || ps.Result == nil {
			continue
		}
		agg.CompletedPasses++
		for _, raw := range ps.Result.Findings {
			f, ok := normalizeFinding(raw)
			if !ok {
				continue
			}
			if f.Confidence == "low" {
				agg.Dropped++
				continue
			}
			merged := false
			for i := range kept {
				if !sameFinding(kept[i].ReviewFinding, f) {
					continue
				}
				k := &kept[i]
				if severityRank[f.Severity] > severityRank[k.Severity] {
					k.Severity = f.Severity
					k.Title = f.Title
				}
				if confidenceRank[f.Confidence] > confidenceRank[k.Confidence] {
					k.Confidence = f.Confidence
				}
				if len(f.Body) > len(k.Body) {
					k.Body = f.Body
				}
				if !containsStr(k.Passes, ps.Name) {
					k.Passes = append(k.Passes, ps.Name)
				}
				merged = true
				break
			}
			if !merged {
				kept = append(kept, aggFinding{ReviewFinding: f, Passes: []string{ps.Name}})
			}
		}
	}
	sort.SliceStable(kept, func(i, j int) bool {
		a, b := kept[i], kept[j]
		if severityRank[a.Severity] != severityRank[b.Severity] {
			return severityRank[a.Severity] > severityRank[b.Severity]
		}
		if a.Path != b.Path {
			return a.Path < b.Path
		}
		return a.Line < b.Line
	})
	agg.Findings = kept
	for _, f := range kept {
		if f.Severity == "blocker" || f.Severity == "major" {
			agg.Outcome = "failure"
			break
		}
	}
	return agg
}

func containsStr(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

func formatAggFinding(f aggFinding) string {
	loc := ""
	if f.Path != "" {
		loc = f.Path
		if f.Line > 0 {
			loc += ":" + strconv.Itoa(f.Line)
		}
		loc = "`" + loc + "` — "
	}
	s := fmt.Sprintf("- **[%s]** %s%s", f.Severity, loc, f.Title)
	if f.Body != "" && f.Body != f.Title {
		s += "\n  " + strings.ReplaceAll(f.Body, "\n", "\n  ")
	}
	return s + " _(passes: " + strings.Join(f.Passes, ", ") + ")_"
}

// renderReviewSummary renders the aggregate as the reviewer's summary markdown.
// On failure it doubles as the feedback the next work run receives.
func renderReviewSummary(agg reviewAggregate) string {
	var must, rest []aggFinding
	for _, f := range agg.Findings {
		if f.Severity == "blocker" || f.Severity == "major" {
			must = append(must, f)
		} else {
			rest = append(rest, f)
		}
	}
	var b strings.Builder
	if agg.Outcome == "failure" {
		fmt.Fprintf(&b, "Review verdict: CHANGES REQUESTED — %d blocking finding(s) across %d focused review passes.\n", len(must), agg.CompletedPasses)
	} else {
		fmt.Fprintf(&b, "Review verdict: APPROVED — no blocking findings across %d focused review passes.\n", agg.CompletedPasses)
	}
	shown := 0
	section := func(title string, fs []aggFinding) {
		if len(fs) == 0 {
			return
		}
		b.WriteString("\n### " + title + "\n")
		for i, f := range fs {
			if shown >= maxReviewSummaryFindings || b.Len() >= maxReviewSummaryBytes {
				fmt.Fprintf(&b, "- …and %d more\n", len(fs)-i)
				return
			}
			b.WriteString(formatAggFinding(f) + "\n")
			shown++
		}
	}
	section("Findings to fix", must)
	section("Minor / nits (non-blocking)", rest)
	var incomplete []string
	for _, p := range agg.Passes {
		if !p.Completed {
			incomplete = append(incomplete, p.Name)
		}
	}
	if len(incomplete) > 0 {
		fmt.Fprintf(&b, "\nIncomplete passes (no findings recorded): %s\n", strings.Join(incomplete, ", "))
	}
	if agg.Dropped > 0 {
		fmt.Fprintf(&b, "\n%d low-confidence finding(s) dropped.\n", agg.Dropped)
	}
	return strings.TrimRight(b.String(), "\n")
}

// renderReviewBreakdown is the per-pass breakdown stored on the run.
func renderReviewBreakdown(agg reviewAggregate) string {
	type row struct {
		Pass     string  `json:"pass"`
		Status   string  `json:"status"`
		Findings int     `json:"findings"`
		Error    string  `json:"error,omitempty"`
		CostUSD  float64 `json:"cost_usd"`
	}
	rows := make([]row, 0, len(agg.Passes))
	for _, p := range agg.Passes {
		r := row{Pass: p.Name, Status: "completed", Error: p.Err, CostUSD: p.CostUSD}
		if !p.Completed {
			r.Status = "incomplete"
		} else if p.Result != nil {
			r.Findings = len(p.Result.Findings)
		}
		rows = append(rows, r)
	}
	data, _ := json.MarshalIndent(map[string]any{
		"review_fanout": rows,
		"kept_findings": len(agg.Findings),
		"dropped_low":   agg.Dropped,
		"final_outcome": agg.Outcome,
	}, "", "  ")
	return string(data)
}
