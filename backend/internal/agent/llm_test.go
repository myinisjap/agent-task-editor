package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestLLMRunner_ExceedsConfiguredMaxTurns verifies that the turn loop bound
// is driven by AgentConfig.MaxTurns (rather than a fixed internal constant):
// with MaxTurns set to 1 and a fake OpenAI-compatible server that always
// responds with a tool call (never signalling completion), Run should
// terminate after exactly one turn with an "exceeded max turns" error.
func TestLLMRunner_ExceedsConfiguredMaxTurns(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"choices": []map[string]any{
				{
					"message": map[string]any{
						"content": "",
						"tool_calls": []map[string]any{
							{
								"id":   "call-1",
								"type": "function",
								"function": map[string]any{
									"name":      "list_files",
									"arguments": `{"path":""}`,
								},
							},
						},
					},
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	runner := &LLMRunner{APIKey: "test-key", BaseURL: srv.URL}
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

// TestLLMRunner_CapturesUsageAndCost verifies that a completed run
// accumulates the prompt/completion token counts reported in the OpenAI-
// compatible response's top-level "usage" field and computes an estimated
// cost from the internal pricing table for a known model ID.
func TestLLMRunner_CapturesUsageAndCost(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"choices": []map[string]any{
				{
					"message": map[string]any{
						"content": "all done",
					},
				},
			},
			"usage": map[string]any{
				"prompt_tokens":     500,
				"completion_tokens": 250,
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	runner := &LLMRunner{APIKey: "test-key", BaseURL: srv.URL}
	logCh := make(chan LogEntry, 256)
	go func() {
		for range logCh {
		}
	}()

	input := RunInput{
		RunID:       "test-run",
		Task:        Task{ID: "task-1", Title: "test task"},
		AgentConfig: AgentConfig{MaxTurns: 5, Model: "gpt-4o-mini"},
		RepoPath:    t.TempDir(),
	}

	res, err := runner.Run(context.Background(), input, logCh)
	close(logCh)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.InputTokens != 500 {
		t.Errorf("want InputTokens=500, got %d", res.InputTokens)
	}
	if res.OutputTokens != 250 {
		t.Errorf("want OutputTokens=250, got %d", res.OutputTokens)
	}
	wantCost := estimateCostUSD("gpt-4o-mini", 500, 250)
	if wantCost <= 0 {
		t.Fatalf("test setup error: expected pricing table to have a non-zero price for gpt-4o-mini")
	}
	if res.CostUSD != wantCost {
		t.Errorf("want CostUSD=%v, got %v", wantCost, res.CostUSD)
	}
}
