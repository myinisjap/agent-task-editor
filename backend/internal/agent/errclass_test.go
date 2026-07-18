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
		{"gemini resource_exhausted", `{"status":"RESOURCE_EXHAUSTED","message":"Quota exceeded"}`, ClassRateLimit},
		{"claude session limit", "You've hit your session limit · resets 6pm (America/Chicago)", ClassRateLimit},
		{"claude usage limit", "You've hit your usage limit for this period", ClassRateLimit},

		// Auth.
		{"not logged in", "Error: Not logged in", ClassAuth},
		{"please run login", "Please run /login to continue", ClassAuth},
		{"gemini invalid api key", "API key not valid. Please pass a valid API key.", ClassAuth},
		{"gemini no auth method", "Please set an Auth method in your settings.json or specify GEMINI_API_KEY", ClassAuth},
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
