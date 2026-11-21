package agent

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func f(sev, conf, p string, line int, title string) ReviewFinding {
	return ReviewFinding{Severity: sev, Confidence: conf, Path: p, Line: line, Title: title, Body: title + " body"}
}

func done(name string, fs ...ReviewFinding) reviewPassOutcome {
	return reviewPassOutcome{Name: name, Completed: true, Result: &ReviewFindingsResult{Summary: "s", Findings: fs}}
}

func TestAggregate_Decision(t *testing.T) {
	tests := []struct {
		name    string
		passes  []reviewPassOutcome
		outcome string
		kept    int
		dropped int
	}{
		{"no findings", []reviewPassOutcome{done("correctness")}, "success", 0, 0},
		{"minor and nit only", []reviewPassOutcome{done("tests", f("minor", "high", "a.go", 1, "x"), f("nit", "high", "b.go", 1, "y"))}, "success", 2, 0},
		{"one major", []reviewPassOutcome{done("correctness", f("major", "high", "a.go", 1, "bug"))}, "failure", 1, 0},
		{"low confidence blocker dropped", []reviewPassOutcome{done("security", f("blocker", "low", "a.go", 1, "maybe"))}, "success", 0, 1},
		{"unknown severity is minor", []reviewPassOutcome{done("tests", f("CRITICAL", "HIGH", "a.go", 1, "x"))}, "success", 1, 0},
		{"unknown confidence is medium", []reviewPassOutcome{done("tests", f("major", "??", "a.go", 1, "x"))}, "failure", 1, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			agg := aggregateReviewFindings(tc.passes)
			if agg.Outcome != tc.outcome || len(agg.Findings) != tc.kept || agg.Dropped != tc.dropped {
				t.Fatalf("got outcome=%s kept=%d dropped=%d", agg.Outcome, len(agg.Findings), agg.Dropped)
			}
		})
	}
}

func TestAggregate_Dedupe(t *testing.T) {
	agg := aggregateReviewFindings([]reviewPassOutcome{
		done("correctness", f("major", "medium", "a.go", 10, "SQL query built from user input")),
		done("security", f("blocker", "high", "a.go", 12, "User input used in SQL query")),
	})
	if len(agg.Findings) != 1 {
		t.Fatalf("want 1 merged finding, got %d", len(agg.Findings))
	}
	g := agg.Findings[0]
	if g.Severity != "blocker" || g.Confidence != "high" || len(g.Passes) != 2 {
		t.Fatalf("bad merge: %+v", g)
	}

	// Different files are not merged.
	agg = aggregateReviewFindings([]reviewPassOutcome{
		done("correctness", f("major", "high", "a.go", 10, "nil deref")),
		done("security", f("major", "high", "b.go", 10, "nil deref")),
	})
	if len(agg.Findings) != 2 {
		t.Fatalf("different files merged: %d", len(agg.Findings))
	}
	// Too far apart.
	agg = aggregateReviewFindings([]reviewPassOutcome{
		done("correctness", f("major", "high", "a.go", 10, "nil deref")),
		done("security", f("major", "high", "a.go", 40, "nil deref")),
	})
	if len(agg.Findings) != 2 {
		t.Fatalf("distant lines merged: %d", len(agg.Findings))
	}
	// Path-less: exact (normalised) title only.
	agg = aggregateReviewFindings([]reviewPassOutcome{
		done("correctness", f("minor", "high", "", 0, "Missing docs")),
		done("conventions", f("minor", "high", "", 0, "missing  docs!")),
		done("tests", f("minor", "high", "", 0, "missing docs for api")),
	})
	if len(agg.Findings) != 2 {
		t.Fatalf("pathless dedupe: got %d", len(agg.Findings))
	}
}

func TestAggregate_EscapingPathsDropped(t *testing.T) {
	agg := aggregateReviewFindings([]reviewPassOutcome{done("security",
		f("minor", "high", "../etc/passwd", 3, "x"), f("minor", "high", "/abs/file", 3, "y"))})
	for _, g := range agg.Findings {
		if g.Path != "" || g.Line != 0 {
			t.Fatalf("escaping path kept: %+v", g)
		}
	}
}

func TestAggregate_IncompletePassDoesNotFail(t *testing.T) {
	agg := aggregateReviewFindings([]reviewPassOutcome{
		done("correctness"),
		{Name: "security", Err: "boom"},
	})
	if agg.Outcome != "success" || agg.CompletedPasses != 1 {
		t.Fatalf("got %+v", agg)
	}
	if s := renderReviewSummary(agg); !strings.Contains(s, "Incomplete passes") || !strings.Contains(s, "security") {
		t.Fatalf("summary missing incomplete pass: %s", s)
	}
}

func TestRenderSummary_VerdictAndOrder(t *testing.T) {
	agg := aggregateReviewFindings([]reviewPassOutcome{done("tests",
		f("nit", "high", "a.go", 1, "nitty"), f("major", "high", "z.go", 9, "serious"))})
	s := renderReviewSummary(agg)
	if !strings.HasPrefix(s, "Review verdict: CHANGES REQUESTED") {
		t.Fatalf("no verdict line: %s", s)
	}
	if strings.Index(s, "serious") > strings.Index(s, "nitty") || !strings.Contains(s, "z.go:9") {
		t.Fatalf("bad ordering/format: %s", s)
	}
	ok := renderReviewSummary(aggregateReviewFindings([]reviewPassOutcome{done("tests")}))
	if !strings.HasPrefix(ok, "Review verdict: APPROVED") {
		t.Fatalf("approved verdict: %s", ok)
	}
}

func TestRenderSummary_Capped(t *testing.T) {
	var fs []ReviewFinding
	for i := 0; i < 80; i++ {
		fs = append(fs, f("minor", "high", "a.go", i*10, "distinct issue number "+strings.Repeat("z", i)+string(rune('a'+i%26))))
	}
	s := renderReviewSummary(aggregateReviewFindings([]reviewPassOutcome{done("tests", fs...)}))
	if !strings.Contains(s, "…and ") {
		t.Fatalf("expected truncation line")
	}
}

func TestShouldFanOutReview(t *testing.T) {
	mk := func(enabled bool, lines, files int64) RunInput {
		return RunInput{AgentConfig: AgentConfig{ReviewFanoutEnabled: enabled, ReviewFanoutMinLines: lines, ReviewFanoutMinFiles: files}}
	}
	reply := "hi"
	withReply := mk(true, 10, 10)
	withReply.HumanReply = &reply
	withSteer := mk(true, 10, 10)
	withSteer.SteeringNote = &reply
	withPR := mk(true, 10, 10)
	withPR.PRReview = &PRReviewInput{}
	tests := []struct {
		name  string
		in    RunInput
		stats diffStats
		want  bool
	}{
		{"disabled", mk(false, 1, 1), diffStats{100, 1000}, false},
		{"below", mk(true, 400, 15), diffStats{14, 399}, false},
		{"lines at threshold", mk(true, 400, 15), diffStats{1, 400}, true},
		{"lines above", mk(true, 400, 15), diffStats{1, 900}, true},
		{"files at threshold", mk(true, 400, 15), diffStats{15, 3}, true},
		{"lines threshold 0 uses files", mk(true, 0, 15), diffStats{14, 5000}, false},
		{"files threshold 0 uses lines", mk(true, 400, 0), diffStats{500, 399}, false},
		{"files threshold 0 lines met", mk(true, 400, 0), diffStats{1, 400}, true},
		{"both 0 always", mk(true, 0, 0), diffStats{0, 0}, true},
		{"human reply", withReply, diffStats{100, 1000}, false},
		{"steering", withSteer, diffStats{100, 1000}, false},
		{"pr review", withPR, diffStats{100, 1000}, false},
	}
	for _, tc := range tests {
		if got := shouldFanOutReview(tc.in, tc.stats); got != tc.want {
			t.Errorf("%s: got %v want %v", tc.name, got, tc.want)
		}
	}
}

func TestDiffNumstatAgainstBase(t *testing.T) {
	dir := initRepo(t)
	run := func(args ...string) {
		t.Helper()
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	run("checkout", "-b", "feat")
	_ = os.WriteFile(filepath.Join(dir, "a.txt"), []byte("1\n2\n3\n"), 0644)
	_ = os.WriteFile(filepath.Join(dir, "README.md"), []byte("changed\n"), 0644)
	_ = os.WriteFile(filepath.Join(dir, "bin.dat"), []byte{0, 1, 2, 0, 0, 255}, 0644)
	run("add", "-A")
	run("commit", "-m", "work")
	st, err := diffNumstatAgainstBase(context.Background(), dir, "main", "feat")
	if err != nil {
		t.Fatal(err)
	}
	// a.txt +3, README +1 -1, binary 0 → 3 files, 5 lines.
	if st.Files != 3 || st.Lines != 5 {
		t.Fatalf("got %+v", st)
	}
	if _, err := diffNumstatAgainstBase(context.Background(), dir, "", "feat"); err == nil {
		t.Fatal("expected error for empty base")
	}
}

// reviewTestProvider scripts each pass by ReviewPass.Name.
type reviewTestProvider struct {
	mu     sync.Mutex
	inputs []RunInput
	byPass map[string]func() (Result, error)
}

func (p *reviewTestProvider) Run(_ context.Context, in RunInput, logCh chan<- LogEntry) (Result, error) {
	p.mu.Lock()
	p.inputs = append(p.inputs, in)
	p.mu.Unlock()
	logCh <- LogEntry{Type: LogStdout, Content: "hello"}
	return p.byPass[in.ReviewPass.Name]()
}

func okPass(cost float64, fs ...ReviewFinding) func() (Result, error) {
	return func() (Result, error) {
		return Result{Status: "completed", CostUSD: cost, InputTokens: 10, OutputTokens: 5,
			ReviewFindings: &ReviewFindingsResult{Summary: "s", Findings: fs}}, nil
	}
}

func runPasses(t *testing.T, prov *reviewTestProvider, in RunInput) (Result, error, []LogEntry) {
	t.Helper()
	logCh := make(chan LogEntry, 64)
	res, err := runReviewPasses(context.Background(), Job{RunID: "r", Input: in, Provider: prov}, "diff", "stat", logCh)
	close(logCh)
	var logs []LogEntry
	for e := range logCh {
		logs = append(logs, e)
	}
	return res, err, logs
}

func baseReviewInput() RunInput {
	return RunInput{
		RunID:              "r",
		ResumeSessionID:    "sess",
		Transitions:        []TransitionHint{{ToLabel: "x"}},
		CostBudgetUSD:      10,
		CostSpentUSD:       2,
		AgentConfig:        AgentConfig{Provider: "claude", SubtasksEnabled: true, CrossRepoSubtasksEnabled: true},
		OpenReviewComments: []ReviewComment{{ID: "c"}},
	}
}

func TestRunReviewPasses_Basics(t *testing.T) {
	prov := &reviewTestProvider{byPass: map[string]func() (Result, error){
		"correctness": okPass(1, f("major", "high", "a.go", 1, "bug")),
		"security":    okPass(2),
		"tests":       okPass(3),
		"conventions": okPass(4),
	}}
	res, err, logs := runPasses(t, prov, baseReviewInput())
	if err != nil {
		t.Fatal(err)
	}
	if len(prov.inputs) != 4 {
		t.Fatalf("want 4 provider calls, got %d", len(prov.inputs))
	}
	seen := map[string]bool{}
	for _, in := range prov.inputs {
		seen[in.ReviewPass.Name] = true
		if in.ResumeSessionID != "" || in.Transitions != nil || in.AgentConfig.SubtasksEnabled || in.AgentConfig.CrossRepoSubtasksEnabled || in.OpenReviewComments != nil {
			t.Errorf("pass input not isolated: %+v", in)
		}
		if in.ReviewPass.Diff != "diff" || in.ReviewPass.Checklist == "" {
			t.Errorf("pass spec incomplete: %+v", in.ReviewPass)
		}
		if want := 2 + 8.0/4; in.CostBudgetUSD != want {
			t.Errorf("budget = %v want %v", in.CostBudgetUSD, want)
		}
	}
	if len(seen) != 4 {
		t.Errorf("passes not distinct: %v", seen)
	}
	if res.Status != "completed" || res.Outcome != "failure" || res.Message == nil || !strings.Contains(*res.Message, "bug") {
		t.Fatalf("bad result: %+v", res)
	}
	if res.CostUSD != 10 || res.InputTokens != 40 || res.OutputTokens != 20 || res.SessionID != "" {
		t.Errorf("bad usage: %+v", res)
	}
	prefixed := 0
	for _, l := range logs {
		if strings.HasPrefix(l.Content, "[review:") {
			prefixed++
		}
	}
	if prefixed != 4 {
		t.Errorf("want 4 prefixed logs, got %d", prefixed)
	}
}

func TestRunReviewPasses_SuccessWhenOnlyNits(t *testing.T) {
	prov := &reviewTestProvider{byPass: map[string]func() (Result, error){
		"correctness": okPass(0, f("nit", "high", "a.go", 1, "nit")),
		"security":    okPass(0), "tests": okPass(0), "conventions": okPass(0),
	}}
	res, err, _ := runPasses(t, prov, baseReviewInput())
	if err != nil || res.Outcome != "success" {
		t.Fatalf("got %+v err=%v", res, err)
	}
}

func TestRunReviewPasses_TransientPropagates(t *testing.T) {
	prov := &reviewTestProvider{byPass: map[string]func() (Result, error){
		"correctness": okPass(0),
		"security":    func() (Result, error) { return Result{}, &ErrTransient{Cause: errors.New("503")} },
		"tests":       okPass(0), "conventions": okPass(0),
	}}
	_, err, _ := runPasses(t, prov, baseReviewInput())
	var te *ErrTransient
	if !errors.As(err, &te) {
		t.Fatalf("want ErrTransient, got %v", err)
	}
}

func TestRunReviewPasses_GenuineErrorUsesRemaining(t *testing.T) {
	prov := &reviewTestProvider{byPass: map[string]func() (Result, error){
		"correctness": okPass(0),
		"security":    func() (Result, error) { return Result{}, errors.New("crashed") },
		"tests":       func() (Result, error) { return Result{Status: "completed"}, nil }, // no findings submitted
		"conventions": okPass(0),
	}}
	res, err, _ := runPasses(t, prov, baseReviewInput())
	if err != nil || res.Status != "completed" || res.Outcome != "success" {
		t.Fatalf("got %+v err=%v", res, err)
	}
	if !strings.Contains(*res.Message, "security") || !strings.Contains(*res.Message, "tests") {
		t.Errorf("incomplete passes not named: %s", *res.Message)
	}
}

func TestRunReviewPasses_AllFail(t *testing.T) {
	bad := func() (Result, error) { return Result{}, errors.New("crashed") }
	prov := &reviewTestProvider{byPass: map[string]func() (Result, error){"correctness": bad, "security": bad, "tests": bad, "conventions": bad}}
	res, err, _ := runPasses(t, prov, baseReviewInput())
	if err == nil || res.Status != "failed" {
		t.Fatalf("want genuine failure, got %+v err=%v", res, err)
	}
	var te transientErr
	if errors.As(err, &te) {
		t.Fatal("must not be transient")
	}
}

func TestRunReviewPasses_SequentialProvider(t *testing.T) {
	in := baseReviewInput()
	in.AgentConfig.Provider = "opencode"
	prov := &reviewTestProvider{byPass: map[string]func() (Result, error){
		"correctness": okPass(0), "security": okPass(0), "tests": okPass(0), "conventions": okPass(0),
	}}
	_, err, _ := runPasses(t, prov, in)
	if err != nil {
		t.Fatal(err)
	}
	for i, p := range reviewPasses {
		if prov.inputs[i].ReviewPass.Name != p.Name {
			t.Fatalf("sequential order broken at %d", i)
		}
	}
}
