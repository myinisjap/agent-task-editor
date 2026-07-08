package agent

import "strings"

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
	// non-zero exit with no infra signal, error_max_turns): no auto-retry,
	// immediate re-dispatch. This is the pool-level default when nothing more
	// specific matched.
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
)

// classPattern is one substring→classification rule. Substr must be lowercase;
// ClassifyLine lowercases the input before matching, so matching is
// case-insensitive.
type classPattern struct {
	Substr string
	Class  Classification
}

// classPatterns is the single source of truth for classifying a raw provider
// output line (CLI stdout/stderr, or the text of a structured error event) by
// substring.
//
// Ordering encodes priority: ClassifyLine returns the FIRST match, so the more
// specific / more actionable classes (rate_limit, auth) are listed before the
// generic transient markers. To adapt to a CLI wording change, add or edit one
// row here and add the corresponding case to TestClassifyLine.
var classPatterns = []classPattern{
	// Rate limiting (HTTP 429). Most specific — checked first so a 429 that
	// also mentions e.g. "timeout" is still classified as a rate limit.
	{"429", ClassRateLimit},
	{"request rejected", ClassRateLimit},
	{"rate limit", ClassRateLimit},
	{"rate_limit", ClassRateLimit},
	// Gemini CLI (Google API) rate-limit signal: gRPC-style status code
	// returned in the JSON error body on quota exhaustion.
	{"resource_exhausted", ClassRateLimit},

	// Authentication / login. Requires a human to re-authenticate, so it must
	// win over the generic transient markers below (an auth failure that also
	// mentions a network hiccup should still escalate, not silently retry).
	{"not logged in", ClassAuth},
	{"please run /login", ClassAuth},
	// Gemini CLI: invalid/missing GEMINI_API_KEY.
	{"api key not valid", ClassAuth},
	// Gemini CLI: no auth method configured at all (fresh, unconfigured host).
	{"please set an auth method", ClassAuth},
	// Codex CLI: expired/missing ChatGPT OAuth session or OPENAI_API_KEY.
	{"missing bearer or basic authentication", ClassAuth},
	{"401 unauthorized", ClassAuth},

	// Transient infrastructure problems (network blips, upstream 5xx, resets,
	// ambiguous timeouts). Least specific — checked last.
	{"connection reset", ClassTransient},
	{"econnreset", ClassTransient},
	{"econnrefused", ClassTransient},
	{"etimedout", ClassTransient},
	{"enotfound", ClassTransient},
	{"eai_again", ClassTransient},
	{"timeout", ClassTransient},
	{"timed out", ClassTransient},
	{"temporary failure", ClassTransient},
	{"network error", ClassTransient},
	{"network is unreachable", ClassTransient},
	{"socket hang up", ClassTransient},
	{"eof", ClassTransient},
	{"502", ClassTransient},
	{"503", ClassTransient},
	{"504", ClassTransient},
	{"bad gateway", ClassTransient},
	{"service unavailable", ClassTransient},
	{"gateway timeout", ClassTransient},
}

// ClassifyLine returns the classification signalled by a single raw output
// line, or ClassNone if the line carries no failure signal. Matching is
// case-insensitive and the first pattern (in classPatterns priority order)
// wins. This is the one place raw provider text is turned into a
// Classification.
func ClassifyLine(line string) Classification {
	lower := strings.ToLower(line)
	for _, p := range classPatterns {
		if strings.Contains(lower, p.Substr) {
			return p.Class
		}
	}
	return ClassNone
}

// is429Line reports whether the line signals an API rate-limit rejection.
// Thin wrapper over ClassifyLine kept for the CLI providers' stdout/stderr
// scan loops.
func is429Line(line string) bool {
	return ClassifyLine(line) == ClassRateLimit
}

// isTransientLine reports whether the line signals a transient infrastructure
// problem (network blip, upstream 5xx, connection reset, timeout) rather than
// a genuine task/agent failure. Thin wrapper over ClassifyLine.
func isTransientLine(line string) bool {
	return ClassifyLine(line) == ClassTransient
}
