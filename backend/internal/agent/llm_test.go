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
