package autonomy

import (
	"fmt"
	"strings"

	"github.com/myinisjap/agent-task-editor/backend/internal/pathglob"
)

// ReasonAskRule is the reason code for a matched ask rule.
const ReasonAskRule = "ask_rule"

// Limits on rule contents.
const (
	MaxRuleGlobs   = 20
	MaxRuleGlobLen = 256
)

// AskRule forces a person to decide at a gate when ALL its set conditions
// match the task. A rule with no conditions never matches.
type AskRule struct {
	ID              string
	RepoID          *string // nil = all repos
	Label           *string // nil = all gates of the workflow
	PathGlobs       []string
	TaskType        *string
	MinChangedLines *int
}

// Facts are the task facts a rule is checked against.
type Facts struct {
	HasDiff      bool
	Files        []string
	ChangedLines int
	TaskType     string
}

// NeedsDiff reports whether matching depends on the diff.
func (r AskRule) NeedsDiff() bool { return len(r.PathGlobs) > 0 || r.MinChangedLines != nil }

// HasCondition reports whether at least one condition is set.
func (r AskRule) HasCondition() bool {
	return len(r.PathGlobs) > 0 || (r.TaskType != nil && strings.TrimSpace(*r.TaskType) != "") || r.MinChangedLines != nil
}

// Matches reports whether every set condition matches. A condition that needs
// the diff does not match when no diff is available.
func (r AskRule) Matches(f Facts, m *pathglob.Matcher) bool {
	if !r.HasCondition() {
		return false
	}
	if r.NeedsDiff() && !f.HasDiff {
		return false
	}
	if len(r.PathGlobs) > 0 {
		if m == nil {
			m = pathglob.NewMatcher()
		}
		if !m.MatchAny(r.PathGlobs, f.Files) {
			return false
		}
	}
	if r.TaskType != nil && strings.TrimSpace(*r.TaskType) != "" && strings.TrimSpace(*r.TaskType) != f.TaskType {
		return false
	}
	if r.MinChangedLines != nil && f.ChangedLines < *r.MinChangedLines {
		return false
	}
	return true
}

// Summary renders the rule for reasons and UIs.
func (r AskRule) Summary() string {
	var parts []string
	if len(r.PathGlobs) > 0 {
		parts = append(parts, "files match "+strings.Join(r.PathGlobs, " or "))
	}
	if r.TaskType != nil && strings.TrimSpace(*r.TaskType) != "" {
		parts = append(parts, "type "+strings.TrimSpace(*r.TaskType))
	}
	if r.MinChangedLines != nil {
		parts = append(parts, fmt.Sprintf("≥ %d changed lines", *r.MinChangedLines))
	}
	if r.Label == nil {
		parts = append(parts, "all gates")
	}
	if r.RepoID == nil {
		parts = append(parts, "all repos")
	}
	return strings.Join(parts, "; ")
}

// Validate checks and normalises the rule's conditions (trims, dedupes globs).
func (r *AskRule) Validate() error {
	seen := map[string]bool{}
	globs := make([]string, 0, len(r.PathGlobs))
	for _, g := range r.PathGlobs {
		g = strings.TrimSpace(g)
		if g == "" {
			return fmt.Errorf("path_globs must not contain empty globs")
		}
		if len(g) > MaxRuleGlobLen {
			return fmt.Errorf("glob %q is longer than %d characters", g, MaxRuleGlobLen)
		}
		if !pathglob.Valid(g) {
			return fmt.Errorf("invalid glob %q", g)
		}
		if !seen[g] {
			seen[g] = true
			globs = append(globs, g)
		}
	}
	if len(globs) > MaxRuleGlobs {
		return fmt.Errorf("at most %d path_globs allowed", MaxRuleGlobs)
	}
	r.PathGlobs = globs
	if r.TaskType != nil {
		t := strings.TrimSpace(*r.TaskType)
		if t == "" {
			r.TaskType = nil
		} else {
			r.TaskType = &t
		}
	}
	if r.MinChangedLines != nil && *r.MinChangedLines < 1 {
		return fmt.Errorf("min_changed_lines must be at least 1")
	}
	if !r.HasCondition() {
		return fmt.Errorf("rule needs at least one condition (path_globs, task_type or min_changed_lines)")
	}
	return nil
}

// MatchRules returns the rules that match f.
func MatchRules(rules []AskRule, f Facts) []AskRule {
	var out []AskRule
	m := pathglob.NewMatcher()
	for _, r := range rules {
		if r.Matches(f, m) {
			out = append(out, r)
		}
	}
	return out
}

// MatchedRuleIDs lists the rule ids behind a decision's ask_rule reasons.
func MatchedRuleIDs(d Decision) []string {
	var ids []string
	for _, r := range d.Reasons {
		if r.Code == ReasonAskRule && r.RuleID != "" {
			ids = append(ids, r.RuleID)
		}
	}
	return ids
}
