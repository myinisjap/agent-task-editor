package agent

import (
	"regexp"
	"strings"
)

// Classification explains *why* an agent run failed. It is the single, explicit
// signal that drives retry/escalation behavior and is logged on every failure
// (as the `classification` field) so a misclassification is diagnosable from
// logs alone.
//
// Historically this decision was spread across three ad-hoc string-sniffing
// sites — login detection in pool.go, plus transient- and rate-limit detection
// in each CLI provider. Any wording change in a CLI release could silently
// break retry/escalation (a failure would degrade to "genuine failure,
// immediate unbounded re-dispatch" or a silent retry loop). Consolidating every
// pattern here means adapting to a CLI-wording change is a one-line edit with a
// matching unit test in errclass_test.go.
type Classification string

const (
	// ClassNone means a line carried no recognizable failure signal. Not a
	// failure category itself — it is the "no match" result of ClassifyLine.
	ClassNone Classification = ""
	// ClassGenuine is a real task/agent failure (the work itself failed, a
	// non-zero exit with no infra signal): no auto-retry, immediate
	// re-dispatch. This is the pool-level default when nothing more specific
	// matched.
	ClassGenuine Classification = "genuine"
	// ClassTransient is an infrastructure blip (network reset, upstream 5xx,
	// ambiguous timeout): bounded auto-retry against the task's retry budget.
	ClassTransient Classification = "transient"
	// ClassRateLimit is an upstream 429: blocks the whole agent config for a
	// backoff window *and* consumes the task's retry budget.
	ClassRateLimit Classification = "rate_limit"
	// ClassAuth is a login/auth failure: escalate to waiting_human so a human
	// can re-authenticate rather than retrying forever.
	ClassAuth Classification = "auth"
	// ClassMaxTurns means the agent hit its configured turn cap
	// (AgentConfig.MaxTurns) without finishing: escalate to waiting_human.
	// Re-dispatching would silently void the operator's cap by handing the
	// next run a fresh turn budget. Applies to the providers that actually
	// enforce the cap (claude, qwen_code, anthropic, llm) — surfaced
	// structurally via a typed error / result subtype, not text-sniffed, so
	// it deliberately has no classPatterns entry below.
	ClassMaxTurns Classification = "max_turns"
	// ClassCostBudget means a provider's mid-run cost watchdog cancelled the
	// run because projected cost crossed the task's effective cost budget
	// (see ErrCostBudgetExceeded): escalate to waiting_human. Surfaced
	// structurally via the typed error, not text-sniffed, so it deliberately
	// has no classPatterns entry below.
	ClassCostBudget Classification = "cost_budget"
)

// classPattern is one match→classification rule: either a plain substring
// match (Substr) or an anchored regexp match (Re) — mutually exclusive,
// ClassifyLine checks Re first when non-nil. Substr must be lowercase.
// ClassifyLine lowercases the input before matching, so matching is
// case-insensitive; Re patterns must therefore be written lowercase and must
// NOT use "(?i)" (redundant and slower).
//
// Re exists for patterns short enough to appear inside ordinary agent
// output rather than only in genuine infra-failure text — e.g. a diff hunk
// header "@@ -429,7 +429,9 @@" would match the bare substring "429", and
// TypeScript's "typeof" would match the bare substring "eof". Anchoring
// these with a regexp (word boundaries and/or an HTTP-status-ish context)
// avoids latching rate_limit/transient on ordinary prose or file contents.
// See issue #335.
type classPattern struct {
	Substr string
	Re     *regexp.Regexp
	Class  Classification
}

// httpStatusRe builds a regexp matching a 3-digit HTTP status code only in an
// HTTP-status-ish context (preceded by "http"/"status"/"code"/"error", or
// followed by a status-phrase like "too many requests"/"bad gateway"), so
// ordinary numbers in agent output — diff hunk headers ("@@ -429,7 +429,9
// @@"), token counts ("1429"), line numbers ("api.ts:429:12") — don't
// false-positive. See issue #335.
func httpStatusRe(code string) *regexp.Regexp {
	return regexp.MustCompile(
		`(?:\bhttp[a-z/. ]*|\bstatus[a-z]*[: ]+|\bcode[: ]+|\berror[: ]+)` + code + `\b` +
			`|\b` + code + `\b\s*(?:too many requests|rate|bad gateway|service unavailable|gateway timeout|error|status)`,
	)
}

// classPatterns is the single source of truth for classifying a raw provider
// output line (CLI stdout/stderr, or the text of a structured error event) by
// anchored substring/regexp match.
//
// Ordering encodes priority: ClassifyLine returns the FIRST match, so the more
// specific / more actionable classes (rate_limit, auth) are listed before the
// generic transient markers. To adapt to a CLI wording change, add or edit one
// row here and add the corresponding case to TestClassifyLine.
var classPatterns = []classPattern{
	// Rate limiting (HTTP 429). Most specific — checked first so a 429 that
	// also mentions e.g. "timeout" is still classified as a rate limit.
	// Anchored to an HTTP-status-ish context (see httpStatusRe) so a diff
	// hunk header ("@@ -429,7 +429,9 @@") or token count ("1429") doesn't
	// false-positive — see issue #335.
	{Re: httpStatusRe("429"), Class: ClassRateLimit},
	{Substr: "request rejected", Class: ClassRateLimit},
	{Substr: "rate limit", Class: ClassRateLimit},
	{Substr: "rate_limit", Class: ClassRateLimit},
	// Claude CLI session/usage limit messages (e.g. "You've hit your session
	// limit · resets 6pm (America/Chicago)") carry no "429"/"rate limit"
	// substring in the result text itself — the 429 lives in the separate
	// api_error_status field instead (see classifyResultMessage in
	// claude.go, which also checks that field directly). Match the wording
	// here too so these are still classified as rate limits when
	// encountered outside that structured field (e.g. in a raw
	// stdout/stderr line).
	{Substr: "session limit", Class: ClassRateLimit},
	{Substr: "usage limit", Class: ClassRateLimit},

	// Authentication / login. Requires a human to re-authenticate, so it must
	// win over the generic transient markers below (an auth failure that also
	// mentions a network hiccup should still escalate, not silently retry).
	{Substr: "not logged in", Class: ClassAuth},
	{Substr: "please run /login", Class: ClassAuth},
	// Codex CLI: expired/missing ChatGPT OAuth session or OPENAI_API_KEY.
	{Substr: "missing bearer or basic authentication", Class: ClassAuth},
	{Substr: "401 unauthorized", Class: ClassAuth},

	// Transient infrastructure problems (network blips, upstream 5xx, resets,
	// ambiguous timeouts). Least specific — checked last.
	{Substr: "connection reset", Class: ClassTransient},
	{Substr: "econnreset", Class: ClassTransient},
	{Substr: "econnrefused", Class: ClassTransient},
	{Substr: "etimedout", Class: ClassTransient},
	{Substr: "enotfound", Class: ClassTransient},
	{Substr: "eai_again", Class: ClassTransient},
	// "timeout" is anchored to a word boundary rather than a bare substring
	// match so identifiers like "timeoutMs"/"socketTimeout" in agent-authored
	// source code don't false-positive — see issue #335.
	{Re: regexp.MustCompile(`\btimeout\b`), Class: ClassTransient},
	{Substr: "timed out", Class: ClassTransient},
	{Substr: "temporary failure", Class: ClassTransient},
	{Substr: "network error", Class: ClassTransient},
	{Substr: "network is unreachable", Class: ClassTransient},
	{Substr: "socket hang up", Class: ClassTransient},
	// "eof" is anchored to a word boundary so it doesn't fire on
	// "typeof"/other identifiers containing the substring — see issue #335.
	{Re: regexp.MustCompile(`\beof\b`), Class: ClassTransient},
	{Re: httpStatusRe("502"), Class: ClassTransient},
	{Re: httpStatusRe("503"), Class: ClassTransient},
	{Re: httpStatusRe("504"), Class: ClassTransient},
	{Substr: "bad gateway", Class: ClassTransient},
	{Substr: "service unavailable", Class: ClassTransient},
	{Substr: "gateway timeout", Class: ClassTransient},
}

// ClassifyLine returns the classification signalled by a single raw output
// line, or ClassNone if the line carries no failure signal. Matching is
// case-insensitive and the first pattern (in classPatterns priority order)
// wins — each pattern is either a plain substring match or, for patterns
// short enough to appear in ordinary agent output, an anchored regexp match
// (see classPattern's doc comment and issue #335). This is the one place raw
// provider text is turned into a Classification.
func ClassifyLine(line string) Classification {
	lower := strings.ToLower(line)
	for _, p := range classPatterns {
		if p.Re != nil {
			if p.Re.MatchString(lower) {
				return p.Class
			}
			continue
		}
		if strings.Contains(lower, p.Substr) {
			return p.Class
		}
	}
	return ClassNone
}
