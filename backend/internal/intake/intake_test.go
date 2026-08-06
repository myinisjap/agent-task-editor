package intake

import (
	"testing"

	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

func strp(s string) *string { return &s }
func i64p(i int64) *int64   { return &i }

func rule(id string, sortOrder int64, mods ...func(*gen.IntakeRule)) gen.IntakeRule {
	r := gen.IntakeRule{
		ID:               id,
		Name:             id,
		Enabled:          true,
		SortOrder:        sortOrder,
		MatchLabels:      "[]",
		MatchAuthorAssoc: "[]",
	}
	for _, m := range mods {
		m(&r)
	}
	return r
}

func TestMatch_FirstMatchWins(t *testing.T) {
	rules := []gen.IntakeRule{
		rule("first", 0, func(r *gen.IntakeRule) { r.MatchSource = "issue" }),
		rule("second", 1, func(r *gen.IntakeRule) { r.MatchSource = "issue" }),
	}
	d, ok := Match(rules, Candidate{Source: "issue"})
	if !ok || d.RuleID != "first" {
		t.Fatalf("expected first rule to win, got %+v ok=%v", d, ok)
	}
}

func TestMatch_SkipsDisabled(t *testing.T) {
	rules := []gen.IntakeRule{
		rule("disabled", 0, func(r *gen.IntakeRule) { r.Enabled = false; r.MatchSource = "issue" }),
		rule("enabled", 1, func(r *gen.IntakeRule) { r.MatchSource = "issue" }),
	}
	d, ok := Match(rules, Candidate{Source: "issue"})
	if !ok || d.RuleID != "enabled" {
		t.Fatalf("expected enabled rule to win, got %+v ok=%v", d, ok)
	}
}

func TestMatch_EmptySourceMatchesAny(t *testing.T) {
	rules := []gen.IntakeRule{rule("any", 0)}
	d, ok := Match(rules, Candidate{Source: "schedule"})
	if !ok || d.RuleID != "any" {
		t.Fatalf("expected empty match_source to match any source, got %+v ok=%v", d, ok)
	}
}

func TestMatch_RepoID(t *testing.T) {
	rules := []gen.IntakeRule{rule("repo-scoped", 0, func(r *gen.IntakeRule) { r.MatchRepoID = strp("repo-1") })}
	if _, ok := Match(rules, Candidate{RepoID: "repo-2"}); ok {
		t.Fatal("expected no match for a different repo")
	}
	if d, ok := Match(rules, Candidate{RepoID: "repo-1"}); !ok || d.RuleID != "repo-scoped" {
		t.Fatalf("expected match for repo-1, got %+v ok=%v", d, ok)
	}
}

func TestMatch_LabelsAnyOfCaseInsensitive(t *testing.T) {
	rules := []gen.IntakeRule{rule("bug", 0, func(r *gen.IntakeRule) { r.MatchLabels = `["Bug","urgent"]` })}
	if _, ok := Match(rules, Candidate{Labels: []string{"enhancement"}}); ok {
		t.Fatal("expected no match: no overlapping label")
	}
	if d, ok := Match(rules, Candidate{Labels: []string{"BUG", "other"}}); !ok || d.RuleID != "bug" {
		t.Fatalf("expected case-insensitive label match, got %+v ok=%v", d, ok)
	}
}

func TestMatch_TitlePattern(t *testing.T) {
	rules := []gen.IntakeRule{rule("crash", 0, func(r *gen.IntakeRule) { r.MatchTitlePattern = `(?i)crash` })}
	if _, ok := Match(rules, Candidate{Title: "app is slow"}); ok {
		t.Fatal("expected no match")
	}
	if d, ok := Match(rules, Candidate{Title: "App CRASHES on load"}); !ok || d.RuleID != "crash" {
		t.Fatalf("expected pattern match, got %+v ok=%v", d, ok)
	}
}

func TestMatch_BodyPattern(t *testing.T) {
	rules := []gen.IntakeRule{rule("stack", 0, func(r *gen.IntakeRule) { r.MatchBodyPattern = `panic:` })}
	if d, ok := Match(rules, Candidate{Body: "we saw a panic: nil pointer"}); !ok || d.RuleID != "stack" {
		t.Fatalf("expected body pattern match, got %+v ok=%v", d, ok)
	}
	if _, ok := Match(rules, Candidate{Body: "no issue here"}); ok {
		t.Fatal("expected no match")
	}
}

func TestMatch_BadRegexpIsInertNotFatal(t *testing.T) {
	rules := []gen.IntakeRule{
		rule("bad", 0, func(r *gen.IntakeRule) { r.MatchTitlePattern = "(unclosed" }),
		rule("fallback", 1),
	}
	d, ok := Match(rules, Candidate{Title: "anything"})
	if !ok || d.RuleID != "fallback" {
		t.Fatalf("expected bad regexp rule skipped, fallback matched; got %+v ok=%v", d, ok)
	}
}

func TestMatch_AuthorAssoc(t *testing.T) {
	rules := []gen.IntakeRule{rule("trusted-only", 0, func(r *gen.IntakeRule) { r.MatchAuthorAssoc = `["OWNER","MEMBER"]` })}
	if _, ok := Match(rules, Candidate{AuthorAssoc: "NONE"}); ok {
		t.Fatal("expected no match for untrusted author")
	}
	if _, ok := Match(rules, Candidate{AuthorAssoc: ""}); ok {
		t.Fatal("expected no match for unknown author association when rule requires one")
	}
	if d, ok := Match(rules, Candidate{AuthorAssoc: "owner"}); !ok || d.RuleID != "trusted-only" {
		t.Fatalf("expected case-insensitive author match, got %+v ok=%v", d, ok)
	}
}

func TestMatch_AllConditionsMustHold(t *testing.T) {
	rules := []gen.IntakeRule{
		rule("strict", 0, func(r *gen.IntakeRule) {
			r.MatchSource = "issue"
			r.MatchLabels = `["bug"]`
			r.MatchTitlePattern = "crash"
		}),
	}
	// Source matches, label matches, title doesn't.
	if _, ok := Match(rules, Candidate{Source: "issue", Labels: []string{"bug"}, Title: "no match here"}); ok {
		t.Fatal("expected AND semantics across fields to reject a partial match")
	}
	if d, ok := Match(rules, Candidate{Source: "issue", Labels: []string{"bug"}, Title: "it will crash"}); !ok || d.RuleID != "strict" {
		t.Fatalf("expected full match, got %+v ok=%v", d, ok)
	}
}

func TestMatch_DecisionFieldsCarried(t *testing.T) {
	rules := []gen.IntakeRule{
		rule("shape", 0, func(r *gen.IntakeRule) {
			r.ApplyTemplateID = strp("tmpl-1")
			r.ApplyPriority = i64p(1)
			r.ApplyTargetLabel = "work"
			r.ApplyWorkflowID = strp("wf-1")
			cost := 5.0
			r.ApplyMaxCostUsd = &cost
		}),
	}
	d, ok := Match(rules, Candidate{})
	if !ok {
		t.Fatal("expected match")
	}
	if d.TemplateID == nil || *d.TemplateID != "tmpl-1" {
		t.Errorf("template id not carried: %+v", d)
	}
	if d.Priority == nil || *d.Priority != 1 {
		t.Errorf("priority not carried: %+v", d)
	}
	if d.TargetLabel != "work" {
		t.Errorf("target label not carried: %+v", d)
	}
	if d.WorkflowID == nil || *d.WorkflowID != "wf-1" {
		t.Errorf("workflow id not carried: %+v", d)
	}
	if d.MaxCostUsd == nil || *d.MaxCostUsd != 5.0 {
		t.Errorf("max cost not carried: %+v", d)
	}
}

func TestAutoStartAllowed(t *testing.T) {
	cases := []struct {
		name        string
		assoc       string
		isGate      bool
		wantAllowed bool
	}{
		{"gate label always allowed", "", true, true},
		{"non-gate no constraint denied", "", false, false},
		{"non-gate with only trusted assoc allowed", `["OWNER"]`, false, true},
		{"non-gate with mixed trust denied", `["OWNER","CONTRIBUTOR"]`, false, false},
		{"non-gate with untrusted only denied", `["NONE"]`, false, false},
		{"non-gate with multiple trusted allowed", `["OWNER","MEMBER","COLLABORATOR"]`, false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := rule("r", 0)
			if tc.assoc != "" {
				r.MatchAuthorAssoc = tc.assoc
			}
			got := AutoStartAllowed(r, tc.isGate)
			if got != tc.wantAllowed {
				t.Errorf("AutoStartAllowed = %v, want %v", got, tc.wantAllowed)
			}
		})
	}
}

func TestValidatePattern(t *testing.T) {
	if err := ValidatePattern(""); err != nil {
		t.Errorf("empty pattern should be valid: %v", err)
	}
	if err := ValidatePattern("(?i)bug"); err != nil {
		t.Errorf("valid pattern rejected: %v", err)
	}
	if err := ValidatePattern("(unclosed"); err == nil {
		t.Error("expected invalid pattern to error")
	}
}

func TestEncodeDecodeStringList(t *testing.T) {
	enc, err := EncodeStringList(nil)
	if err != nil || enc != "[]" {
		t.Fatalf("expected [] for nil, got %q err=%v", enc, err)
	}
	enc, err = EncodeStringList([]string{"a", "b"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := DecodeStringList(enc)
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("round-trip failed: %v", got)
	}
	if DecodeStringList("") != nil {
		t.Error("expected nil for empty string")
	}
	if DecodeStringList("not json") != nil {
		t.Error("expected nil for invalid JSON")
	}
}
