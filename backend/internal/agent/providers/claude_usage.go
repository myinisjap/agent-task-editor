package providers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// claudeUsageBaseURL is the base URL for Anthropic's OAuth usage endpoint.
// Overridable in tests.
var claudeUsageBaseURL = "https://api.anthropic.com"

// claudeUsageHTTPClient is a short-timeout client so a hung/unreachable
// Anthropic API never stalls a dashboard load.
var claudeUsageHTTPClient = &http.Client{Timeout: 10 * time.Second}

// ErrNoClaudeCredentials is returned by FetchClaudeUsage when no Claude
// OAuth access token is available (e.g. ~/.claude/.credentials.json is
// missing, unreadable, or has no accessToken — for example when using an
// API-key-only setup rather than `claude login`).
var ErrNoClaudeCredentials = errors.New("no claude oauth credentials available")

// ClaudeUsage holds the current account's rate-limit utilization for the
// rolling 5-hour window and the weekly (7-day) window, as reported by
// Anthropic's OAuth usage endpoint.
type ClaudeUsage struct {
	FiveHourPercent  float64
	FiveHourResetsAt *time.Time
	WeeklyPercent    float64
	WeeklyResetsAt   *time.Time
}

// claudeUsageWindow mirrors one utilization window in the
// /api/oauth/usage response body.
type claudeUsageWindow struct {
	Utilization float64 `json:"utilization"`
	ResetsAt    string  `json:"resets_at"`
}

type claudeUsageResponseBody struct {
	FiveHour *claudeUsageWindow `json:"five_hour"`
	SevenDay *claudeUsageWindow `json:"seven_day"`
}

// FetchClaudeUsage fetches the current Claude account's rate-limit
// utilization from Anthropic's OAuth usage endpoint using the OAuth access
// token found in ~/.claude/.credentials.json.
//
// Returns ErrNoClaudeCredentials if no OAuth token is available (distinct
// from a transient/network failure) so callers can distinguish "not logged
// in via `claude login`" from "the request failed" and degrade gracefully
// in both cases without ever failing the caller's own request.
func FetchClaudeUsage(ctx context.Context) (*ClaudeUsage, error) {
	token := ClaudeOAuthAccessToken()
	if token == "" {
		return nil, ErrNoClaudeCredentials
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, claudeUsageBaseURL+"/api/oauth/usage", nil)
	if err != nil {
		return nil, fmt.Errorf("build usage request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("anthropic-beta", "oauth-2025-04-20")

	resp, err := claudeUsageHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("claude usage request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusTooManyRequests {
		return nil, fmt.Errorf("claude usage request rate limited (429)")
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return nil, fmt.Errorf("claude usage request failed: http %d: %s", resp.StatusCode, string(body))
	}

	var body claudeUsageResponseBody
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("decode claude usage response: %w", err)
	}

	usage := &ClaudeUsage{}
	if body.FiveHour != nil {
		usage.FiveHourPercent = clampPercent(body.FiveHour.Utilization)
		usage.FiveHourResetsAt = parseUsageResetsAt(body.FiveHour.ResetsAt)
	}
	if body.SevenDay != nil {
		usage.WeeklyPercent = clampPercent(body.SevenDay.Utilization)
		usage.WeeklyResetsAt = parseUsageResetsAt(body.SevenDay.ResetsAt)
	}
	return usage, nil
}

// clampPercent clamps a utilization value to [0, 100].
func clampPercent(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return v
}

// parseUsageResetsAt parses an RFC3339 timestamp, returning nil if empty
// or unparseable.
func parseUsageResetsAt(s string) *time.Time {
	if s == "" {
		return nil
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return nil
	}
	return &t
}
