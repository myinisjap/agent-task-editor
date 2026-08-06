// Package intake implements the match->apply rule engine evaluated at
// task-creation time for the 'issue' and 'schedule' sources (see migration
// 051 and docs/task-sources.md). It is a leaf package: it imports only
// internal/storage/gen and the standard library, so tasksource, schedule,
// and the API handlers can all depend on it with no import cycle risk (the
// same discipline internal/forge follows).
//
// # Matching semantics
//
// Rules are evaluated first-match-wins, walking the enabled rule set in
// sort_order (then created_at) order — the first rule whose MATCH
// conditions all hold wins; no rule after it is considered, and no
// "accumulate" behaviour exists. This mirrors how the dispatcher walks
// matchConfigs.
//
//   - An empty/unset match field means "no constraint" (matches anything)
//     for that dimension: match_source == "", match_repo_id == nil,
//     match_labels == [], match_title_pattern == "", match_body_pattern ==
//     "", match_author_assoc == [].
//   - match_labels and match_author_assoc are ANY-of, case-insensitive
//     (a Candidate matches if at least one of its labels/its author
//     association case-insensitively equals one entry in the rule's list).
//     Label case-insensitivity accounts for external trackers varying in
//     casing conventions.
//   - match_title_pattern and match_body_pattern are plain Go regexps
//     (regexp.MatchString semantics — unanchored substring search unless
//     the author anchors it themselves). They are NOT rewritten to be
//     case-insensitive automatically; write "(?i)" yourself if you want
//     that. Patterns are compiled once by the CRUD handler at write time
//     (to reject a bad regexp with a 400 immediately) and independently
//     compiled-and-cached here at match time, keyed by pattern string, so a
//     rule that somehow reached the matcher with an invalid pattern (e.g. a
//     direct DB edit) can never crash or abort a sweep — it is logged and
//     treated as non-matching instead.
//   - All specified conditions on a rule must ALL hold (AND across match
//     fields; OR within a list-valued field) for the rule to match.
package intake

import (
	"encoding/json"
	"log/slog"
	"regexp"
	"strings"
	"sync"

	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

// Candidate is the information available about an incoming item at the
// point a rule sweep needs to decide how to shape it. Source is one of
// "manual", "issue", "schedule", "subtask" (only "issue" and "schedule" are
// currently evaluated against rules — see the package doc on
// tasksource/schedule integration). RepoID is the target repo. AuthorAssoc
// is the reporting item's author association as reported by the forge
// (OWNER/MEMBER/COLLABORATOR/CONTRIBUTOR/NONE/...), empty when unknown or
// not applicable (e.g. a schedule firing has no "author").
type Candidate struct {
	Source      string
	RepoID      string
	Title       string
	Body        string
	Labels      []string
	AuthorAssoc string
}

// Decision is what a matched rule wants applied to the task being created.
// Every field is optional (nil/"" = "the caller should apply its own
// default"); RuleID is always populated when Decision comes from a match.
type Decision struct {
	RuleID      string
	TemplateID  *string
	Priority    *int64
	TargetLabel string
	WorkflowID  *string
	MaxCostUsd  *float64
}

// regexCache memoizes compiled patterns across Match calls, so a sweep over
// many candidates against the same rule set doesn't recompile the same
// regexp per item. Safe for concurrent use; the intake package's callers
// (tasksource.Importer, schedule.Scheduler) each sweep from a single
// goroutine, but a shared package-level cache is kept safe regardless.
type regexCache struct {
	mu    sync.Mutex
	cache map[string]*regexp.Regexp
}

func (c *regexCache) compile(pattern string) (*regexp.Regexp, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cache == nil {
		c.cache = make(map[string]*regexp.Regexp)
	}
	if re, ok := c.cache[pattern]; ok {
		return re, nil
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		// Cache the failure too (as a nil entry marker via a distinct map)
		// would need a second map; simplest to just not cache errors, since
		// a bad pattern should be rare (rejected at write time) and
		// recompiling it every time it's hit is not a hot path.
		return nil, err
	}
	c.cache[pattern] = re
	return re, nil
}

var defaultRegexCache = &regexCache{}

// Match walks rules in the order given (callers are expected to have
// already ordered by sort_order via ListEnabledIntakeRules, and to have
// already filtered to enabled rules) and returns the Decision from the
// first rule whose MATCH conditions all hold against c. ok is false if no
// rule matches, in which case the caller should fall back to its own
// defaults entirely.
func Match(rules []gen.IntakeRule, c Candidate) (Decision, bool) {
	for _, rule := range rules {
		if !rule.Enabled {
			continue
		}
		if matches(rule, c) {
			return toDecision(rule), true
		}
	}
	return Decision{}, false
}

func matches(rule gen.IntakeRule, c Candidate) bool {
	if rule.MatchSource != "" && !strings.EqualFold(rule.MatchSource, c.Source) {
		return false
	}
	if rule.MatchRepoID != nil && *rule.MatchRepoID != "" && *rule.MatchRepoID != c.RepoID {
		return false
	}
	if !matchesAnyOfCI(rule.MatchLabels, c.Labels) {
		return false
	}
	if !matchesAuthorAssoc(rule.MatchAuthorAssoc, c.AuthorAssoc) {
		return false
	}
	if rule.MatchTitlePattern != "" && !matchesPattern(rule.MatchTitlePattern, c.Title) {
		return false
	}
	if rule.MatchBodyPattern != "" && !matchesPattern(rule.MatchBodyPattern, c.Body) {
		return false
	}
	return true
}

// matchesAnyOfCI reports whether jsonList (a JSON array of strings, or ""/
// "[]" for "any") is empty (matches anything) or contains at least one
// entry that case-insensitively equals one of candidateValues.
func matchesAnyOfCI(jsonList string, candidateValues []string) bool {
	values := decodeStringList(jsonList)
	if len(values) == 0 {
		return true // no constraint
	}
	for _, want := range values {
		for _, got := range candidateValues {
			if strings.EqualFold(want, got) {
				return true
			}
		}
	}
	return false
}

// matchesAuthorAssoc is matchesAnyOfCI specialised to a single candidate
// value (an item has exactly one author association, not a list).
func matchesAuthorAssoc(jsonList string, assoc string) bool {
	values := decodeStringList(jsonList)
	if len(values) == 0 {
		return true // no constraint
	}
	if assoc == "" {
		// The rule requires a specific association but the candidate has
		// none reported (e.g. forge doesn't support it, or a non-issue
		// source) — treat as non-matching rather than guessing trust.
		return false
	}
	for _, want := range values {
		if strings.EqualFold(want, assoc) {
			return true
		}
	}
	return false
}

func decodeStringList(jsonList string) []string {
	if jsonList == "" || jsonList == "[]" {
		return nil
	}
	var out []string
	if err := json.Unmarshal([]byte(jsonList), &out); err != nil {
		return nil
	}
	return out
}

// matchesPattern compiles (or fetches from cache) pattern and reports
// whether it matches text. A compile failure at match time (which should
// only happen if a rule bypassed CRUD-time validation, e.g. a direct DB
// edit) is logged once and treated as non-matching — it must never crash or
// abort a sweep.
func matchesPattern(pattern, text string) bool {
	re, err := defaultRegexCache.compile(pattern)
	if err != nil {
		slog.Warn("intake: rule has invalid regexp; treating as non-matching", "pattern", pattern, "err", err)
		return false
	}
	return re.MatchString(text)
}

func toDecision(rule gen.IntakeRule) Decision {
	return Decision{
		RuleID:      rule.ID,
		TemplateID:  rule.ApplyTemplateID,
		Priority:    rule.ApplyPriority,
		TargetLabel: rule.ApplyTargetLabel,
		WorkflowID:  rule.ApplyWorkflowID,
		MaxCostUsd:  rule.ApplyMaxCostUsd,
	}
}

// TrustedAssociations is the set of author associations considered trusted
// enough to let a rule land a task directly on an agent-triggerable label
// without a human review step in between. Exported so the CRUD handler's
// validation error message and any UI copy can enumerate the same list
// without duplicating it.
var TrustedAssociations = []string{"OWNER", "MEMBER", "COLLABORATOR"}

// AutoStartAllowed reports whether rule may land a task directly on an
// agent-triggerable (non-agent_ignore) label rather than the workflow's
// human-review gate label.
//
// This is the single most important safety property in the intake-rules
// feature: imported issue bodies are untrusted input (see #331), and
// sweepRepo has always landed every import on the gate label specifically
// so a human promotes it before an agent ever acts on it. A rule that sets
// apply_target_label to a non-gate label removes that mitigation, so it is
// only allowed when the rule also constrains match_author_assoc to a
// non-empty list drawn entirely from TrustedAssociations — i.e. the rule
// itself asserts "only auto-start for issues filed by someone we already
// trust".
//
// isTargetGateLabel is true when apply_target_label (or the caller's
// fallback) is the workflow's gate/agent_ignore label — landing there is
// always safe regardless of author association, since it still waits for a
// human. Callers must pass the *resolved* label (rule's apply_target_label
// if set, otherwise whatever gate the caller would have used) evaluated
// against the *effective* workflow (see the apply_workflow_id override
// caveat in tasksource.Importer).
func AutoStartAllowed(rule gen.IntakeRule, isTargetGateLabel bool) bool {
	if isTargetGateLabel {
		return true
	}
	assocs := decodeStringList(rule.MatchAuthorAssoc)
	if len(assocs) == 0 {
		return false
	}
	for _, a := range assocs {
		if !isTrustedAssociation(a) {
			return false
		}
	}
	return true
}

func isTrustedAssociation(assoc string) bool {
	for _, t := range TrustedAssociations {
		if strings.EqualFold(t, assoc) {
			return true
		}
	}
	return false
}

// ValidatePattern compiles pattern (a Go regexp) purely to validate it,
// discarding the result — used by the CRUD handler at rule create/update
// time so an invalid pattern is rejected with a 400 immediately rather than
// silently becoming an inert (never-matching) rule at sweep time.
func ValidatePattern(pattern string) error {
	if pattern == "" {
		return nil
	}
	_, err := regexp.Compile(pattern)
	return err
}

// EncodeStringList JSON-encodes values for storage in a match_labels /
// match_author_assoc column. A nil or empty slice encodes to "[]" (the
// column's "any" sentinel), never to "null".
func EncodeStringList(values []string) (string, error) {
	if len(values) == 0 {
		return "[]", nil
	}
	b, err := json.Marshal(values)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// DecodeStringList is the exported counterpart to decodeStringList, for
// callers (API handlers, preview) that need to read a match_labels /
// match_author_assoc column back into a Go slice.
func DecodeStringList(jsonList string) []string {
	return decodeStringList(jsonList)
}
