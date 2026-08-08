package agent

import "testing"

// TestClassifyLine exercises one representative line per pattern in the central
// classification table plus the priority ordering between classes. Adding a new
// row to classPatterns should come with a new case here so a CLI-wording change
// stays a one-line edit with test coverage.
func TestClassifyLine(t *testing.T) {
	cases := []struct {
		name string
		line string
		want Classification
	}{
		// Rate limiting.
		{"http 429", `{"error":"http 429: too many requests"}`, ClassRateLimit},
		{"request rejected", "Request rejected by API", ClassRateLimit},
		{"rate limit words", "you hit the rate limit", ClassRateLimit},
		{"rate_limit token", "error type rate_limit_error", ClassRateLimit},
		{"claude session limit", "You've hit your session limit · resets 6pm (America/Chicago)", ClassRateLimit},
		{"claude usage limit", "You've hit your usage limit for this period", ClassRateLimit},

		// Auth.
		{"not logged in", "Error: Not logged in", ClassAuth},
		{"please run login", "Please run /login to continue", ClassAuth},
		{"codex missing bearer", "unexpected status 401 Unauthorized: Missing bearer or basic authentication in header", ClassAuth},
		{"codex 401", "HTTP error: 401 Unauthorized, url: wss://api.openai.com/v1/responses", ClassAuth},

		// Transient.
		{"connection reset", "read: connection reset by peer", ClassTransient},
		{"econnreset", "Error: ECONNRESET", ClassTransient},
		{"econnrefused", "connect ECONNREFUSED 127.0.0.1", ClassTransient},
		{"etimedout", "ETIMEDOUT", ClassTransient},
		{"enotfound", "getaddrinfo ENOTFOUND api.example.com", ClassTransient},
		{"eai_again", "getaddrinfo EAI_AGAIN", ClassTransient},
		{"timeout", "request timeout", ClassTransient},
		{"timed out", "the operation timed out", ClassTransient},
		{"temporary failure", "temporary failure in name resolution", ClassTransient},
		{"network error", "a network error occurred", ClassTransient},
		{"network unreachable", "connect: network is unreachable", ClassTransient},
		{"socket hang up", "socket hang up", ClassTransient},
		{"eof", "unexpected EOF", ClassTransient},
		{"502", "received HTTP 502 from upstream", ClassTransient},
		{"503", "HTTP 503", ClassTransient},
		{"504", "status 504", ClassTransient},
		{"bad gateway", "502 Bad Gateway", ClassTransient},
		{"service unavailable", "503 Service Unavailable", ClassTransient},
		{"gateway timeout", "504 Gateway Timeout", ClassTransient},

		// No signal.
		{"plain text", "compiling package foo", ClassNone},
		{"empty", "", ClassNone},

		// No signal — issue #335 false positives. Short patterns like
		// "429"/"eof"/"timeout" are anchored (word boundary and/or
		// HTTP-status context) so they don't fire on ordinary agent output:
		// diff hunks, source code, token counts, line numbers, port numbers.
		{"diff hunk header", "@@ -429,7 +429,9 @@ func foo() {", ClassNone},
		{"diff hunk header 502", "@@ -502,3 +502,7 @@", ClassNone},
		{"typescript typeof", "const t = typeof x === 'string'", ClassNone},
		{"timeout in source code", "  const timeoutMs = 30_000; // socketTimeout", ClassNone},
		{"token count containing 429", "total tokens: 1429 input, 87 output", ClassNone},
		// Not distinguishable from "status 429" by a lightweight regexp
		// without also rejecting genuine "status 429" text; the colon after
		// "ts" combined with digits makes this look line-number-shaped, but
		// httpStatusRe's "status" context only requires the literal word
		// "status", so this correctly stays ClassNone (no "http"/"status"/
		// "code"/"error" keyword nor a trailing status phrase precedes/
		// follows the 429).
		{"line number", "src/api.ts:429:12 error TS2345", ClassNone},
		{"port number", "listening on port 5029", ClassNone},

		// Positive regression cases proving the anchored patterns still fire.
		{"429 too many requests", "Error: 429 Too Many Requests", ClassRateLimit},
		{"http 502 upstream", "received HTTP 502 from upstream", ClassTransient},
		// ClassMaxTurns is deliberately NOT in classPatterns: it is a
		// structural signal (subtype/typed error), not text-sniffed, so a raw
		// line merely mentioning "max turns" must not be classified by
		// ClassifyLine — see classifyResultMessage in
		// providers/parse_streamjson.go for the actual structural check.
		{"max turns text is not structurally classified", "exceeded max turns (50)", ClassNone},

		// Case-insensitivity.
		{"upper rate limit", "RATE LIMIT EXCEEDED", ClassRateLimit},
		{"upper not logged in", "NOT LOGGED IN", ClassAuth},

		// Priority: a 429 that also mentions a transient marker is a rate limit.
		{"429 wins over timeout", "429 rate limit; request timed out", ClassRateLimit},
		// Priority: an auth failure that also mentions a network hiccup escalates.
		{"auth wins over transient", "Not logged in (connection reset)", ClassAuth},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ClassifyLine(tc.line); got != tc.want {
				t.Errorf("ClassifyLine(%q) = %q, want %q", tc.line, got, tc.want)
			}
		})
	}
}
