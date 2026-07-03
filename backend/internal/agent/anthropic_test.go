package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestAnthropicRunner_ExceedsConfiguredMaxTurns verifies that the turn loop
// bound is driven by AgentConfig.MaxTurns (rather than a fixed internal
// constant): with MaxTurns set to 1 and a fake server that always responds
// with a tool_use block (never signalling completion), Run should terminate
// after exactly one turn with an "exceeded max turns" error.
func TestAnthropicRunner_ExceedsConfiguredMaxTurns(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := anthropicResponse{
			Content: []anthropicContent{
				{
					Type:  "tool_use",
					ID:    "tool-1",
					Name:  "list_files",
					Input: json.RawMessage(`{"path":""}`),
				},
			},
			StopReason: "tool_use",
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	runner := &AnthropicRunner{APIKey: "test-key", BaseURL: srv.URL}
	logCh := make(chan LogEntry, 256)
	go func() {
		for range logCh {
		}
	}()

	input := RunInput{
		RunID:       "test-run",
		Task:        Task{ID: "task-1", Title: "test task"},
		AgentConfig: AgentConfig{MaxTurns: 1},
		RepoPath:    t.TempDir(),
	}

	_, err := runner.Run(context.Background(), input, logCh)
	close(logCh)
	if err == nil {
		t.Fatalf("expected error from exceeding max turns, got nil")
	}
	if !strings.Contains(err.Error(), "exceeded max turns (1)") {
		t.Fatalf("expected 'exceeded max turns (1)' error, got: %v", err)
	}
}

// TestAnthropicRunner_CapturesUsageAndCost verifies that a completed run
// accumulates the input/output token counts reported in the Messages API
// response's "usage" field and computes an estimated cost from the internal
// pricing table for a known model ID.
func TestAnthropicRunner_CapturesUsageAndCost(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := anthropicResponse{
			Content: []anthropicContent{
				{Type: "text", Text: "all done"},
			},
			StopReason: "end_turn",
			Usage: &struct {
				InputTokens  int64 `json:"input_tokens"`
				OutputTokens int64 `json:"output_tokens"`
			}{InputTokens: 1000, OutputTokens: 2000},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	runner := &AnthropicRunner{APIKey: "test-key", BaseURL: srv.URL}
	logCh := make(chan LogEntry, 256)
	go func() {
		for range logCh {
		}
	}()

	input := RunInput{
		RunID:       "test-run",
		Task:        Task{ID: "task-1", Title: "test task"},
		AgentConfig: AgentConfig{MaxTurns: 5, Model: "claude-sonnet-4-5"},
		RepoPath:    t.TempDir(),
	}

	res, err := runner.Run(context.Background(), input, logCh)
	close(logCh)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.InputTokens != 1000 {
		t.Errorf("want InputTokens=1000, got %d", res.InputTokens)
	}
	if res.OutputTokens != 2000 {
		t.Errorf("want OutputTokens=2000, got %d", res.OutputTokens)
	}
	wantCost := estimateCostUSD("claude-sonnet-4-5", 1000, 2000)
	if wantCost <= 0 {
		t.Fatalf("test setup error: expected pricing table to have a non-zero price for claude-sonnet-4-5")
	}
	if res.CostUSD != wantCost {
		t.Errorf("want CostUSD=%v, got %v", wantCost, res.CostUSD)
	}
}
