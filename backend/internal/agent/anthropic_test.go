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

// TestAnthropicRunner_SignalCompleteReadsOutcome is a regression test for a
// bug where anthropicTools advertised signal_complete's parameter as
// "next_label" but executeLLMTool actually read "outcome" — meaning any
// well-behaved model following the schema it was given would see its
// completion signal silently dropped (Result.Outcome stayed empty, which
// pool.go's resolveOutcome cannot map to a transition). This verifies the
// full loop now reads "outcome" per the (now-corrected) tool schema and
// surfaces it on the returned Result.
func TestAnthropicRunner_SignalCompleteReadsOutcome(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := anthropicResponse{
			Content: []anthropicContent{
				{
					Type:  "tool_use",
					ID:    "tool-1",
					Name:  "signal_complete",
					Input: json.RawMessage(`{"outcome":"success","summary":"all done"}`),
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
		AgentConfig: AgentConfig{MaxTurns: 5},
		RepoPath:    t.TempDir(),
	}

	res, err := runner.Run(context.Background(), input, logCh)
	close(logCh)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Status != "completed" {
		t.Fatalf("want Status=completed, got %q", res.Status)
	}
	if res.Outcome != "success" {
		t.Fatalf("want Outcome=success (read from the 'outcome' arg), got %q", res.Outcome)
	}
	if res.Message == nil || *res.Message != "all done" {
		t.Fatalf("unexpected message: %v", res.Message)
	}
}

// TestAnthropicRunner_GetTaskTransitions verifies that input.Transitions is
// threaded through to the native get_task_transitions tool and returned to
// the model as JSON, matching the MCP sidecar's output shape.
func TestAnthropicRunner_GetTaskTransitions(t *testing.T) {
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		var resp anthropicResponse
		if callCount == 1 {
			resp = anthropicResponse{
				Content: []anthropicContent{
					{Type: "tool_use", ID: "tool-1", Name: "get_task_transitions", Input: json.RawMessage(`{}`)},
				},
				StopReason: "tool_use",
			}
		} else {
			resp = anthropicResponse{
				Content:    []anthropicContent{{Type: "text", Text: "done"}},
				StopReason: "end_turn",
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	runner := &AnthropicRunner{APIKey: "test-key", BaseURL: srv.URL}
	logCh := make(chan LogEntry, 256)
	var toolResults []string
	done := make(chan struct{})
	go func() {
		defer close(done)
		for entry := range logCh {
			if entry.Type == LogToolResult {
				toolResults = append(toolResults, entry.Content)
			}
		}
	}()

	input := RunInput{
		RunID:       "test-run",
		Task:        Task{ID: "task-1", Title: "test task"},
		AgentConfig: AgentConfig{MaxTurns: 5},
		RepoPath:    t.TempDir(),
		Transitions: []TransitionHint{{ToLabel: "review", Path: "success"}},
	}

	_, err := runner.Run(context.Background(), input, logCh)
	close(logCh)
	<-done
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(toolResults) != 1 {
		t.Fatalf("expected exactly one tool result, got %d: %v", len(toolResults), toolResults)
	}
	var got []TransitionHint
	if err := json.Unmarshal([]byte(toolResults[0]), &got); err != nil {
		t.Fatalf("expected valid JSON transitions, got %q: %v", toolResults[0], err)
	}
	if len(got) != 1 || got[0].ToLabel != "review" {
		t.Fatalf("unexpected transitions: %+v", got)
	}
}
