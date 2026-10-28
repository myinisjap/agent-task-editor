package handlers_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/myinisjap/agent-task-editor/backend/internal/api/handlers"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

type btEnv struct {
	r      http.Handler
	q      *gen.Queries
	sqlDB  *sql.DB
	wfID   string
	repoID string
	repo   string
}

func newBacktestEnv(t *testing.T) *btEnv {
	t.Helper()
	db := openTestDB(t)
	q := gen.New(db.SQL())
	wfs, _ := q.ListWorkflows(context.Background())
	wfID := wfs[0].ID
	repo := t.TempDir()
	repoID := uuid.NewString()
	if _, err := q.CreateRepo(context.Background(), gen.CreateRepoParams{ID: repoID, Name: "r", Path: repo, WorkflowID: &wfID}); err != nil {
		t.Fatal(err)
	}
	r := chi.NewRouter()
	r.Post("/workflows/{id}/labels/{label}/autonomy/backtest", handlers.NewAutonomyHandler(q).Backtest)
	return &btEnv{r: r, q: q, sqlDB: db.SQL(), wfID: wfID, repoID: repoID, repo: repo}
}

func (e *btEnv) post(t *testing.T, label string, body any) *httptest.ResponseRecorder {
	t.Helper()
	b, _ := json.Marshal(body)
	w := httptest.NewRecorder()
	e.r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/workflows/"+e.wfID+"/labels/"+label+"/autonomy/backtest", bytes.NewReader(b)))
	return w
}

// pass seeds a task with one exit from review at `ago` ago.
func (e *btEnv) pass(t *testing.T, title, to, trigger, note string, ago time.Duration, cost float64, verify string, alwaysAsk bool) string {
	t.Helper()
	ctx := context.Background()
	task, err := e.q.CreateTask(ctx, gen.CreateTaskParams{ID: uuid.NewString(), Title: title, WorkflowID: e.wfID, RepoID: e.repoID, Label: "done"})
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC().Add(-ago).Format("2006-01-02 15:04:05")
	if alwaysAsk {
		e.exec(t, `UPDATE tasks SET always_ask=1 WHERE id=?`, task.ID)
	}
	runID := uuid.NewString()
	e.exec(t, `INSERT INTO agent_runs (id, task_id, status, cost_usd, created_at) VALUES (?, ?, 'completed', ?, ?)`,
		runID, task.ID, cost, time.Now().UTC().Add(-ago-time.Hour).Format("2006-01-02 15:04:05"))
	if verify != "" {
		e.exec(t, `INSERT INTO run_verifications (run_id, task_id, command, status, output) VALUES (?, ?, 'x', ?, '')`, runID, task.ID, verify)
	}
	e.exec(t, `INSERT INTO task_label_history (id, task_id, from_label, to_label, trigger, note, created_at) VALUES (?, ?, 'review', ?, ?, NULLIF(?, ''), ?)`,
		uuid.NewString(), task.ID, to, trigger, note, at)
	return task.ID
}

func (e *btEnv) exec(t *testing.T, q string, args ...any) {
	t.Helper()
	if _, err := e.sqlDB.Exec(q, args...); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
}

type btResp struct {
	Total, Evaluated, Unknown, WouldSkip, WouldAsk, Rejected, Escaped int
	Truncated                                                         bool
	Passes                                                            []struct {
		TaskID    string   `json:"task_id"`
		Title     string   `json:"title"`
		Outcome   string   `json:"outcome"`
		Flags     []string `json:"flags"`
		WouldSkip *bool    `json:"would_skip"`
		Unknown   bool     `json:"unknown"`
	}
}

func (e *btEnv) run(t *testing.T, body any) btResp {
	t.Helper()
	w := e.post(t, "review", body)
	if w.Code != http.StatusOK {
		t.Fatalf("code %d: %s", w.Code, w.Body)
	}
	var m map[string]json.RawMessage
	_ = json.Unmarshal(w.Body.Bytes(), &m)
	var out struct {
		Total     int  `json:"total"`
		Evaluated int  `json:"evaluated"`
		Unknown   int  `json:"unknown"`
		WouldSkip int  `json:"would_skip"`
		WouldAsk  int  `json:"would_ask"`
		Rejected  int  `json:"rejected"`
		Escaped   int  `json:"escaped"`
		Truncated bool `json:"truncated"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	var resp btResp
	_ = json.Unmarshal(m["passes"], &resp.Passes)
	resp.Total, resp.Evaluated, resp.Unknown = out.Total, out.Evaluated, out.Unknown
	resp.WouldSkip, resp.WouldAsk, resp.Rejected, resp.Escaped, resp.Truncated = out.WouldSkip, out.WouldAsk, out.Rejected, out.Escaped, out.Truncated
	return resp
}

func TestBacktest_AutoMode(t *testing.T) {
	e := newBacktestEnv(t)
	h := time.Hour
	e.pass(t, "clean-approved", "done", "human", "", 5*h, 1, "pass", false)
	e.pass(t, "failed-verify-rejected", "work", "human", "", 4*h, 1, "fail", false)
	e.pass(t, "clean-rejected", "work", "human", "", 3*h, 1, "", false)
	e.pass(t, "expensive", "done", "human", "", 2*h, 9, "pass", false)
	e.pass(t, "always-ask", "done", "human", "", 1*h, 1, "pass", true)
	e.pass(t, "forced", "done", "human", "Forced move: x", 30*time.Minute, 1, "", false)
	e.pass(t, "agent-move", "done", "agent", "", 30*time.Minute, 1, "", false)
	e.pass(t, "old", "done", "human", "", 40*24*h, 1, "", false)

	max := 5.0
	got := e.run(t, map[string]any{"mode": "auto", "max_cost_usd": max})
	if got.Total != 5 || got.Evaluated != 5 || got.Unknown != 0 {
		t.Fatalf("counts: %+v", got)
	}
	if got.WouldSkip != 2 || got.WouldAsk != 3 || got.Rejected != 2 || got.Escaped != 1 {
		t.Fatalf("tallies: %+v", got)
	}
	wantTitles := []string{"clean-approved", "failed-verify-rejected", "clean-rejected", "expensive", "always-ask"}
	for i, p := range got.Passes {
		if p.Title != wantTitles[i] {
			t.Fatalf("order[%d]=%s want %s", i, p.Title, wantTitles[i])
		}
	}
	if !*got.Passes[2].WouldSkip || got.Passes[2].Outcome != "rejected" || *got.Passes[1].WouldSkip {
		t.Fatalf("passes: %+v", got.Passes)
	}

	// 90 day window includes the old pass; ask mode skips nothing.
	got = e.run(t, map[string]any{"mode": "ask", "days": 90})
	if got.Total != 6 || got.WouldSkip != 0 || got.WouldAsk != 6 || got.Escaped != 0 {
		t.Fatalf("ask: %+v", got)
	}
}

func git(t *testing.T, dir string, env []string, args ...string) {
	t.Helper()
	c := exec.Command("git", args...)
	c.Dir = dir
	c.Env = append(os.Environ(), append(env, "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")...)
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v %s", args, err, out)
	}
}

func TestBacktest_OnFlagsAsOf(t *testing.T) {
	e := newBacktestEnv(t)
	git(t, e.repo, nil, "init", "-q", "-b", "main")
	_ = os.WriteFile(filepath.Join(e.repo, "a.txt"), []byte("a\n"), 0o644)
	git(t, e.repo, nil, "add", ".")
	git(t, e.repo, []string{"GIT_COMMITTER_DATE=2020-01-01T00:00:00Z", "GIT_AUTHOR_DATE=2020-01-01T00:00:00Z"}, "commit", "-qm", "base")
	git(t, e.repo, nil, "checkout", "-q", "-b", "feat")
	_ = os.WriteFile(filepath.Join(e.repo, "go.mod"), []byte("module x\n"), 0o644)
	git(t, e.repo, nil, "add", ".")
	old := time.Now().UTC().Add(-48 * time.Hour).Format(time.RFC3339)
	git(t, e.repo, []string{"GIT_COMMITTER_DATE=" + old, "GIT_AUTHOR_DATE=" + old}, "commit", "-qm", "deps")
	// A later commit that should NOT be seen by the pass made 24h ago.
	_ = os.WriteFile(filepath.Join(e.repo, "later.txt"), []byte("l\n"), 0o644)
	git(t, e.repo, nil, "add", ".")
	git(t, e.repo, nil, "commit", "-qm", "later")

	withBranch := e.pass(t, "deps", "done", "human", "", 24*time.Hour, 0, "", false)
	e.exec(t, `INSERT INTO task_git (task_id, branch, base_ref) VALUES (?, 'feat', 'main')`, withBranch)
	noBranch := e.pass(t, "gone", "done", "human", "", 23*time.Hour, 0, "", false)
	e.exec(t, `INSERT INTO task_git (task_id, branch, base_ref) VALUES (?, 'deleted-branch', 'main')`, noBranch)
	noDiff := e.pass(t, "nodiff", "work", "human", "", 22*time.Hour, 0, "", false)
	e.exec(t, `INSERT INTO task_git (task_id, branch, base_ref) VALUES (?, 'main', 'main')`, noDiff)

	got := e.run(t, map[string]any{"mode": "on_flags", "ask_on": []string{"dependencies"}})
	if got.Total != 3 || got.Unknown != 1 || got.Evaluated != 2 || got.WouldAsk != 2 || got.WouldSkip != 0 {
		t.Fatalf("on_flags: %+v", got)
	}
	if len(got.Passes[0].Flags) != 1 || got.Passes[0].Flags[0] != "dependencies" {
		t.Fatalf("flags: %+v", got.Passes[0])
	}
	if !got.Passes[1].Unknown || got.Passes[1].WouldSkip != nil {
		t.Fatalf("unknown: %+v", got.Passes[1])
	}
	// Not asking on dependencies: the deps pass would be skipped; nodiff still asks.
	got = e.run(t, map[string]any{"mode": "on_flags", "ask_on": []string{"migration"}})
	if got.WouldSkip != 1 || got.WouldAsk != 1 || got.Unknown != 1 {
		t.Fatalf("on_flags2: %+v", got)
	}
}

func TestBacktest_Validation(t *testing.T) {
	e := newBacktestEnv(t)
	for name, tc := range map[string]struct {
		label string
		body  any
		code  int
	}{
		"bad mode":    {"review", map[string]any{"mode": "nope"}, 400},
		"bad flag":    {"review", map[string]any{"mode": "on_flags", "ask_on": []string{"zzz"}}, 400},
		"days high":   {"review", map[string]any{"mode": "auto", "days": 91}, 400},
		"not a gate":  {"work", map[string]any{"mode": "auto"}, 400},
		"unknown lbl": {"nope", map[string]any{"mode": "auto"}, 404},
	} {
		if w := e.post(t, tc.label, tc.body); w.Code != tc.code {
			t.Errorf("%s: got %d want %d: %s", name, w.Code, tc.code, w.Body)
		}
	}
	got := e.run(t, map[string]any{"mode": "auto"})
	if got.Total != 0 || got.Passes == nil {
		t.Fatalf("empty: %+v", got)
	}
}

func TestBacktest_ExtraRules(t *testing.T) {
	e := newBacktestEnv(t)
	h := time.Hour
	bug := e.pass(t, "bug", "done", "human", "", 5*h, 0, "pass", false)
	e.pass(t, "feature", "done", "human", "", 4*h, 0, "pass", false)
	e.exec(t, `UPDATE tasks SET type='bug' WHERE id=?`, bug)
	// Policy approvals at the gate for both tasks.
	var ids []string
	if err := func() error {
		rows, err := e.sqlDB.Query(`SELECT id FROM tasks`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id string
			_ = rows.Scan(&id)
			ids = append(ids, id)
		}
		return nil
	}(); err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		e.exec(t, `INSERT INTO autonomy_decisions (id, task_id, workflow_id, label, mode, decision, reasons, outcome, outcome_at)
			VALUES (?, ?, ?, 'review', 'auto', 'approve', '[]', 'approved', CURRENT_TIMESTAMP)`, uuid.NewString(), id, e.wfID)
	}

	plain := e.post(t, "review", map[string]any{"mode": "auto"})
	if plain.Code != 200 || !bytes.Contains(plain.Body.Bytes(), []byte(`"rules":null`)) {
		t.Fatalf("no extra_rules: %d %s", plain.Code, plain.Body)
	}
	if e.run(t, map[string]any{"mode": "auto"}).WouldSkip != 2 {
		t.Fatal("baseline would_skip should be 2")
	}

	w := e.post(t, "review", map[string]any{"mode": "auto", "extra_rules": []map[string]any{{"task_type": "bug"}, {"task_type": "chore"}}})
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	var got struct {
		WouldSkip int `json:"would_skip"`
		WouldAsk  int `json:"would_ask"`
		Rules     *struct {
			PolicyApprovals int `json:"policy_approvals"`
			Evaluated       int `json:"evaluated"`
			WouldStop       int `json:"would_stop"`
			Stopped         []struct {
				Title       string `json:"title"`
				RuleIndexes []int  `json:"rule_indexes"`
			} `json:"stopped"`
		} `json:"rules"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if got.WouldSkip != 1 || got.WouldAsk != 1 {
		t.Fatalf("extra rule should flip the bug pass: %s", w.Body)
	}
	if got.Rules == nil || got.Rules.PolicyApprovals != 2 || got.Rules.Evaluated != 2 || got.Rules.WouldStop != 1 ||
		got.Rules.Stopped[0].Title != "bug" || len(got.Rules.Stopped[0].RuleIndexes) != 1 || got.Rules.Stopped[0].RuleIndexes[0] != 0 {
		t.Fatalf("rules = %s", w.Body)
	}
	// Stored rules apply too, and nothing was counted as a hit.
	e.exec(t, `INSERT INTO autonomy_ask_rules (id, workflow_id, label, task_type) VALUES ('r1', ?, 'review', 'bug')`, e.wfID)
	if got := e.run(t, map[string]any{"mode": "auto"}); got.WouldSkip != 1 {
		t.Fatalf("stored rule: %+v", got)
	}
	var hits int
	_ = e.sqlDB.QueryRow(`SELECT hit_count FROM autonomy_ask_rules WHERE id='r1'`).Scan(&hits)
	if hits != 0 {
		t.Fatalf("backtest recorded hits: %d", hits)
	}
	// Invalid extra rule.
	for _, body := range []any{
		map[string]any{"mode": "auto", "extra_rules": []map[string]any{{}}},
		map[string]any{"mode": "auto", "extra_rules": []map[string]any{{"task_type": "bug"}, {"min_changed_lines": 0}}},
	} {
		if w := e.post(t, "review", body); w.Code != 400 {
			t.Fatalf("invalid extra rule: %d %s", w.Code, w.Body)
		}
	}
}
