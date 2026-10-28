package autonomy

import (
	"reflect"
	"strings"
	"testing"
)

func sp(s string) *string { return &s }
func ip(i int) *int       { return &i }

func TestAskRuleMatches(t *testing.T) {
	facts := Facts{HasDiff: true, Files: []string{"src/db/a.go", "migrations/1.sql"}, ChangedLines: 120, TaskType: "bug"}
	tests := []struct {
		name string
		r    AskRule
		f    Facts
		want bool
	}{
		{"no conditions", AskRule{}, facts, false},
		{"glob dir", AskRule{PathGlobs: []string{"src/db/**"}}, facts, true},
		{"glob basename ext", AskRule{PathGlobs: []string{"*.sql"}}, facts, true},
		{"glob trailing slash", AskRule{PathGlobs: []string{"migrations/"}}, facts, true},
		{"glob miss", AskRule{PathGlobs: []string{"web/**"}}, facts, false},
		{"any of globs", AskRule{PathGlobs: []string{"web/**", "*.sql"}}, facts, true},
		{"type match", AskRule{TaskType: sp("bug")}, facts, true},
		{"type mismatch", AskRule{TaskType: sp("feature")}, facts, false},
		{"min ok", AskRule{MinChangedLines: ip(120)}, facts, true},
		{"min too big", AskRule{MinChangedLines: ip(121)}, facts, false},
		{"AND all", AskRule{PathGlobs: []string{"*.sql"}, TaskType: sp("bug"), MinChangedLines: ip(100)}, facts, true},
		{"AND one fails", AskRule{PathGlobs: []string{"*.sql"}, TaskType: sp("feature")}, facts, false},
		{"glob without diff", AskRule{PathGlobs: []string{"*.sql"}}, Facts{TaskType: "bug"}, false},
		{"min without diff", AskRule{MinChangedLines: ip(1)}, Facts{}, false},
		{"type without diff", AskRule{TaskType: sp("bug")}, Facts{TaskType: "bug"}, true},
	}
	for _, c := range tests {
		if got := c.r.Matches(c.f, nil); got != c.want {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
	if (AskRule{PathGlobs: []string{"a"}}).NeedsDiff() != true || (AskRule{TaskType: sp("x")}).NeedsDiff() {
		t.Error("NeedsDiff wrong")
	}
}

func TestAskRuleValidate(t *testing.T) {
	r := AskRule{PathGlobs: []string{" a/** ", "a/**", "*.sql"}, TaskType: sp(" bug ")}
	if err := r.Validate(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(r.PathGlobs, []string{"a/**", "*.sql"}) || *r.TaskType != "bug" {
		t.Fatalf("not normalised: %+v", r)
	}
	long := strings.Repeat("a", 257)
	many := make([]string, 21)
	for i := range many {
		many[i] = "d" + strings.Repeat("x", i) + "/**"
	}
	bad := []AskRule{
		{},
		{TaskType: sp("  ")},
		{PathGlobs: []string{""}},
		{PathGlobs: []string{long}},
		{PathGlobs: many},
		{MinChangedLines: ip(0)},
	}
	for i, b := range bad {
		if err := b.Validate(); err == nil {
			t.Errorf("case %d: expected error", i)
		}
	}
}

func TestAskRuleSummary(t *testing.T) {
	r := AskRule{PathGlobs: []string{"src/db/**", "*.sql"}, TaskType: sp("bug"), MinChangedLines: ip(300)}
	want := "files match src/db/** or *.sql; type bug; ≥ 300 changed lines; all gates; all repos"
	if got := r.Summary(); got != want {
		t.Fatalf("got %q", got)
	}
	r.Label, r.RepoID = sp("review"), sp("r1")
	if strings.Contains(r.Summary(), "all") {
		t.Fatalf("scoped rule: %q", r.Summary())
	}
}

func TestEvaluateAskRule(t *testing.T) {
	rule := AskRule{ID: "r1", PathGlobs: []string{"*.sql"}}
	miss := AskRule{ID: "r2", PathGlobs: []string{"*.go"}}
	facts := Facts{HasDiff: true, Files: []string{"a.sql"}}
	for _, mode := range []Mode{ModeAuto, ModeOnFlags} {
		d := Evaluate(Input{Mode: mode, HasDiff: true, Rules: []AskRule{rule, miss}, Facts: facts})
		if d.Decision != DecisionAsk || !reflect.DeepEqual(codes(d), []string{ReasonAskRule}) {
			t.Fatalf("%s: %+v", mode, d)
		}
		if ids := MatchedRuleIDs(d); !reflect.DeepEqual(ids, []string{"r1"}) {
			t.Fatalf("ids %v", ids)
		}
	}
	if d := Evaluate(Input{Mode: ModeAuto, Rules: []AskRule{miss}, Facts: facts}); d.Decision != DecisionApprove {
		t.Fatalf("non-matching rule changed outcome: %+v", d)
	}
	if d := Evaluate(Input{Mode: ModeAsk, Rules: []AskRule{rule}, Facts: facts}); !reflect.DeepEqual(codes(d), []string{ReasonModeAsk}) {
		t.Fatalf("ask mode: %+v", d)
	}
}
