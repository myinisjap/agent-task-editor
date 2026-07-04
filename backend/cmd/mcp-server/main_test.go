package main

import (
	"encoding/json"
	"testing"
)

func newTestState(commentIDs ...string) *toolState {
	st := &toolState{commentIDs: map[string]bool{}}
	for _, id := range commentIDs {
		st.commentIDs[id] = true
	}
	return st
}

func TestResolveComment_AccumulatesAndPersistsPartialResult(t *testing.T) {
	st := newTestState("c-1", "c-2")

	_, r := dispatchTool("resolve_comment", json.RawMessage(`{"comment_id":"c-1","note":"fixed"}`), st, nil)
	if r == nil {
		t.Fatal("expected a partial result to persist after resolve_comment")
	}
	if r.Status != "" {
		t.Errorf("partial result should carry no terminal status, got %q", r.Status)
	}
	if len(r.ResolvedComments) != 1 || r.ResolvedComments[0].ID != "c-1" || r.ResolvedComments[0].Note != "fixed" {
		t.Errorf("unexpected resolved comments: %+v", r.ResolvedComments)
	}

	// Duplicate resolution is a no-op.
	text, r2 := dispatchTool("resolve_comment", json.RawMessage(`{"comment_id":"c-1","note":"again"}`), st, nil)
	if r2 != nil {
		t.Errorf("duplicate resolve should not persist, got %+v", r2)
	}
	if text != "comment already resolved" {
		t.Errorf("unexpected text: %q", text)
	}
}

func TestResolveComment_RejectsUnknownID(t *testing.T) {
	st := newTestState("c-1")
	text, r := dispatchTool("resolve_comment", json.RawMessage(`{"comment_id":"nope","note":"x"}`), st, nil)
	if r != nil {
		t.Errorf("unknown id should not persist a result, got %+v", r)
	}
	if len(st.resolved) != 0 {
		t.Errorf("unknown id should not be recorded, got %+v", st.resolved)
	}
	if text == "" || text == "comment resolved" {
		t.Errorf("expected an error message, got %q", text)
	}
}

func TestSignalComplete_IncludesResolutions(t *testing.T) {
	st := newTestState("c-1")
	_, _ = dispatchTool("resolve_comment", json.RawMessage(`{"comment_id":"c-1","note":"fixed"}`), st, nil)
	_, r := dispatchTool("signal_complete", json.RawMessage(`{"outcome":"success","summary":"done"}`), st, nil)
	if r == nil {
		t.Fatal("expected terminal result")
	}
	if r.Status != "completed" || r.Outcome != "success" {
		t.Errorf("unexpected terminal result: %+v", r)
	}
	if len(r.ResolvedComments) != 1 || r.ResolvedComments[0].ID != "c-1" {
		t.Errorf("terminal result missing resolutions: %+v", r.ResolvedComments)
	}
}

func TestResolveComment_AfterSignalComplete_RewritesTerminalResult(t *testing.T) {
	st := newTestState("c-1", "c-2")
	_, _ = dispatchTool("signal_complete", json.RawMessage(`{"outcome":"success","summary":"done"}`), st, nil)
	_, r := dispatchTool("resolve_comment", json.RawMessage(`{"comment_id":"c-2","note":"late fix"}`), st, nil)
	if r == nil {
		t.Fatal("expected the terminal result to be re-persisted")
	}
	if r.Status != "completed" {
		t.Errorf("late resolve should preserve terminal status, got %q", r.Status)
	}
	if len(r.ResolvedComments) != 1 || r.ResolvedComments[0].ID != "c-2" {
		t.Errorf("unexpected resolutions on terminal result: %+v", r.ResolvedComments)
	}
}
