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

		// Auth.
		{"not logged in", "Error: Not logged in", ClassAuth},
		{"please run login", "Please run /login to continue", ClassAuth},

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

// TestIs429AndTransientWrappers verifies the thin bool wrappers the CLI
// providers use stay consistent with ClassifyLine.
func TestIs429AndTransientWrappers(t *testing.T) {
	if !is429Line("HTTP 429 rate limit") {
		t.Error("is429Line should be true for a 429 line")
	}
	if is429Line("connection reset by peer") {
		t.Error("is429Line should be false for a transient line")
	}
	if !isTransientLine("connection reset by peer") {
		t.Error("isTransientLine should be true for a transient line")
	}
	if isTransientLine("HTTP 429 rate limit") {
		t.Error("isTransientLine should be false for a rate-limit line")
	}
	// An auth line is neither a 429 nor a transient signal at the provider
	// level — the pool escalates it separately via hasLoginError.
	if is429Line("Not logged in") || isTransientLine("Not logged in") {
		t.Error("auth line must not be classified as 429 or transient")
	}
}

// TestClassifyResultMessage verifies the structured stream-json "result"
// classification preferred by the claude/qwen providers.
func TestClassifyResultMessage(t *testing.T) {
	cases := []struct {
		name string
		line string
		want Classification
	}{
		{
			name: "clean success",
			line: `{"type":"result","subtype":"success","result":"OUTCOME: success"}`,
			want: ClassNone,
		},
		{
			name: "error_max_turns is genuine (no infra signal)",
			line: `{"type":"result","subtype":"error_max_turns","is_error":true,"result":"reached max turns"}`,
			want: ClassNone,
		},
		{
			name: "error with rate-limit text",
			line: `{"type":"result","subtype":"error","is_error":true,"result":"API error: 429 rate limit"}`,
			want: ClassRateLimit,
		},
		{
			name: "error with transient text",
			line: `{"type":"result","subtype":"error","is_error":true,"result":"connection reset by peer"}`,
			want: ClassTransient,
		},
		{
			name: "is_error flag without error subtype",
			line: `{"type":"result","subtype":"","is_error":true,"result":"upstream 503 service unavailable"}`,
			want: ClassTransient,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, _, got, _ := classifyStreamJSON(tc.line)
			if got != tc.want {
				t.Errorf("classifyStreamJSON(%q) classification = %q, want %q", tc.line, got, tc.want)
			}
		})
	}
}

// TestClassifyStreamJSON_NonResultNoClassification verifies non-result message
// types never carry a failure classification (only the typed terminal result
// event does).
func TestClassifyStreamJSON_NonResultNoClassification(t *testing.T) {
	for _, line := range []string{
		`{"type":"assistant","message":{"content":[{"type":"text","text":"connection reset"}]}}`,
		`{"type":"tool_use"}`,
		`{"type":"tool_result"}`,
		`not json at all`,
	} {
		if _, _, _, class, _ := classifyStreamJSON(line); class != ClassNone {
			t.Errorf("line %q: want ClassNone, got %q", line, class)
		}
	}
}
