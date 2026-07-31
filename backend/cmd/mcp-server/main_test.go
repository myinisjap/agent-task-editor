package main

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
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

func TestDispatchTool_GetTaskTransitions(t *testing.T) {
	st := newTestState()

	text, r := dispatchTool("get_task_transitions", nil, st, nil)
	if r != nil {
		t.Errorf("expected no persisted result, got %+v", r)
	}
	if text != "No transitions configured for this label." {
		t.Errorf("unexpected text with no transitions: %q", text)
	}

	transitions := []transitionHint{{ToLabel: "review", Path: "success"}}
	text, r = dispatchTool("get_task_transitions", nil, st, transitions)
	if r != nil {
		t.Errorf("expected no persisted result, got %+v", r)
	}
	var got []transitionHint
	if err := json.Unmarshal([]byte(text), &got); err != nil {
		t.Fatalf("expected JSON transitions, got %q: %v", text, err)
	}
	if len(got) != 1 || got[0].ToLabel != "review" {
		t.Errorf("unexpected transitions: %+v", got)
	}
}

func TestDispatchTool_RequestHuman(t *testing.T) {
	st := newTestState()
	text, r := dispatchTool("request_human", json.RawMessage(`{"message":"need a decision"}`), st, nil)
	if r == nil {
		t.Fatal("expected a terminal result")
	}
	if r.Status != "waiting_human" {
		t.Errorf("status = %q, want waiting_human", r.Status)
	}
	if r.Message == nil || *r.Message != "need a decision" {
		t.Errorf("unexpected message: %+v", r.Message)
	}
	if text != "pausing for human input" {
		t.Errorf("unexpected text: %q", text)
	}
	if st.terminal != r {
		t.Errorf("expected st.terminal to be set to the returned result")
	}
}

func TestDispatchTool_StoreInfo(t *testing.T) {
	st := newTestState()
	text, r := dispatchTool("store_info", json.RawMessage(`{"info":"some context"}`), st, nil)
	if r != nil {
		t.Errorf("store_info should not persist a terminal/partial result, got %+v", r)
	}
	if text != "stored" {
		t.Errorf("unexpected text: %q", text)
	}
	if st.storedInfo != "some context" {
		t.Errorf("storedInfo = %q, want %q", st.storedInfo, "some context")
	}
}

func TestDispatchTool_UpdateTaskNotes(t *testing.T) {
	st := newTestState()
	_, _ = dispatchTool("update_task_notes", json.RawMessage(`{"notes":"first"}`), st, nil)
	if st.notes != "first" {
		t.Fatalf("notes = %q, want %q", st.notes, "first")
	}

	// Append=false replaces.
	_, _ = dispatchTool("update_task_notes", json.RawMessage(`{"notes":"replaced"}`), st, nil)
	if st.notes != "replaced" {
		t.Fatalf("notes = %q, want %q", st.notes, "replaced")
	}

	// Append=true concatenates with a blank line.
	text, _ := dispatchTool("update_task_notes", json.RawMessage(`{"notes":"more","append":true}`), st, nil)
	if st.notes != "replaced\n\nmore" {
		t.Fatalf("notes = %q, want %q", st.notes, "replaced\n\nmore")
	}
	if text != "Task notes updated" {
		t.Errorf("unexpected text: %q", text)
	}
}

func TestDispatchTool_ResolveComment_RequiresID(t *testing.T) {
	st := newTestState("c-1")
	text, r := dispatchTool("resolve_comment", json.RawMessage(`{"note":"x"}`), st, nil)
	if r != nil {
		t.Errorf("expected no persisted result, got %+v", r)
	}
	if text != "comment_id is required" {
		t.Errorf("unexpected text: %q", text)
	}
}

func TestDispatchTool_UnknownTool(t *testing.T) {
	st := newTestState()
	text, r := dispatchTool("not_a_real_tool", nil, st, nil)
	if r != nil {
		t.Errorf("expected no result for unknown tool, got %+v", r)
	}
	if text != "unknown tool: not_a_real_tool" {
		t.Errorf("unexpected text: %q", text)
	}
}

func TestSignalComplete_IncludesNotesAndStoredInfo(t *testing.T) {
	st := newTestState()
	_, _ = dispatchTool("update_task_notes", json.RawMessage(`{"notes":"hello"}`), st, nil)
	_, _ = dispatchTool("store_info", json.RawMessage(`{"info":"ctx"}`), st, nil)
	_, r := dispatchTool("signal_complete", json.RawMessage(`{"outcome":"success","summary":"done"}`), st, nil)
	if r == nil {
		t.Fatal("expected terminal result")
	}
	if r.Notes == nil || *r.Notes != "hello" {
		t.Errorf("unexpected notes: %+v", r.Notes)
	}
	if r.StoredInfo == nil || *r.StoredInfo != "ctx" {
		t.Errorf("unexpected stored info: %+v", r.StoredInfo)
	}
}

func TestCreateSubtask_NotConfigured(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	got := createSubtask(subtaskConfig{}, json.RawMessage(`{"title":"x"}`), log)
	if got != "create_subtask is not configured on this server" {
		t.Errorf("unexpected text: %q", got)
	}
}

func TestCreateSubtask_RequiresTitle(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := subtaskConfig{enabled: true, backendURL: "http://example.invalid", taskID: "task-1"}

	got := createSubtask(cfg, json.RawMessage(`{"description":"no title here"}`), log)
	if got != "title is required" {
		t.Errorf("unexpected text: %q", got)
	}
}

func TestCreateSubtask_Success(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	var gotAuth, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"new-task-id","label":"todo"}`))
	}))
	defer srv.Close()

	cfg := subtaskConfig{enabled: true, backendURL: srv.URL, taskID: "task-1", apiToken: "secret-token"}
	got := createSubtask(cfg, json.RawMessage(`{"title":"Do the thing","description":"desc","type":"chore"}`), log)

	if got != `Created subtask new-task-id on label "todo".` {
		t.Errorf("unexpected text: %q", got)
	}
	if gotAuth != "Bearer secret-token" {
		t.Errorf("unexpected Authorization header: %q", gotAuth)
	}
	if !strings.Contains(gotBody, `"Do the thing"`) {
		t.Errorf("expected request body to include the title, got %q", gotBody)
	}
}

func TestCreateSubtask_BackendError(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"subtask cap reached"}`))
	}))
	defer srv.Close()

	cfg := subtaskConfig{enabled: true, backendURL: srv.URL, taskID: "task-1"}
	got := createSubtask(cfg, json.RawMessage(`{"title":"x"}`), log)

	want := `create_subtask failed (400): subtask cap reached`
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestCreateSubtask_BackendErrorWithoutMessage(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	cfg := subtaskConfig{enabled: true, backendURL: srv.URL, taskID: "task-1"}
	got := createSubtask(cfg, json.RawMessage(`{"title":"x"}`), log)

	want := `create_subtask failed (500)`
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestCreateSubtask_UnreachableBackend(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := subtaskConfig{enabled: true, backendURL: "http://127.0.0.1:1", taskID: "task-1"}

	got := createSubtask(cfg, json.RawMessage(`{"title":"x"}`), log)
	if !strings.HasPrefix(got, "failed to reach backend: ") {
		t.Errorf("unexpected text: %q", got)
	}
}
