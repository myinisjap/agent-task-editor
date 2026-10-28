package escape

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/myinisjap/agent-task-editor/backend/internal/storage"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
	"github.com/myinisjap/agent-task-editor/backend/internal/wsevent"
)

type pubs struct {
	mu sync.Mutex
	ev []string
}

func (p *pubs) Publish(t string, _ any) { p.mu.Lock(); p.ev = append(p.ev, t); p.mu.Unlock() }
func (p *pubs) count(t string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	n := 0
	for _, e := range p.ev {
		if e == t {
			n++
		}
	}
	return n
}

type env struct {
	q    *gen.Queries
	rec  *Recorder
	pub  *pubs
	wfID string
	repo string
	now  time.Time
}

func setup(t *testing.T) *env {
	t.Helper()
	db, err := storage.Open(t.TempDir() + "/s.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()
	if err := storage.SeedDefaultWorkflow(ctx, db); err != nil {
		t.Fatal(err)
	}
	q := gen.New(db.SQL())
	wfs, _ := q.ListWorkflows(ctx)
	repo := uuid.NewString()
	if _, err := q.CreateRepo(ctx, gen.CreateRepoParams{ID: repo, Name: "r", Path: t.TempDir(), WorkflowID: &wfs[0].ID}); err != nil {
		t.Fatal(err)
	}
	e := &env{q: q, pub: &pubs{}, wfID: wfs[0].ID, repo: repo, now: time.Now().UTC()}
	e.rec = New(q, e.pub)
	e.rec.SetNow(func() time.Time { return e.now })
	return e
}

func (e *env) setMode(t *testing.T, label, mode, askOn string) {
	t.Helper()
	ls, _ := e.q.ListWorkflowLabels(context.Background(), e.wfID)
	for _, l := range ls {
		if l.Name == label {
			if err := e.q.SetWorkflowLabelAutonomy(context.Background(), gen.SetWorkflowLabelAutonomyParams{
				AutonomyMode: mode, AutonomyAskOn: askOn, AutonomyHoldSeconds: 0, ID: l.ID}); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func (e *env) mode(t *testing.T, label string) (string, string) {
	ls, _ := e.q.ListWorkflowLabels(context.Background(), e.wfID)
	for _, l := range ls {
		if l.Name == label {
			return l.AutonomyMode, l.AutonomyAskOn
		}
	}
	t.Fatal("no label")
	return "", ""
}

// escape records an escape against a fresh task and a fresh policy decision.
func (e *env) escape(t *testing.T, decisionID *string) (*gen.AutonomyChange, string) {
	t.Helper()
	ctx := context.Background()
	task, err := e.q.CreateTask(ctx, gen.CreateTaskParams{ID: uuid.NewString(), Title: "t", WorkflowID: e.wfID, RepoID: e.repo, Label: "review"})
	if err != nil {
		t.Fatal(err)
	}
	if decisionID == nil {
		id := uuid.NewString()
		decisionID = &id
	}
	_, ch, err := e.rec.Record(ctx, Input{TaskID: task.ID, WorkflowID: e.wfID, Label: "review", Source: "undo", DecisionID: decisionID})
	if err != nil {
		t.Fatal(err)
	}
	return ch, *decisionID
}

func TestUnderThresholdDoesNothing(t *testing.T) {
	e := setup(t)
	e.setMode(t, "review", "auto", "[]")
	if ch, _ := e.escape(t, nil); ch != nil {
		t.Fatal("stepped on first escape")
	}
	if m, _ := e.mode(t, "review"); m != "auto" {
		t.Fatal(m)
	}
}

func TestStepDownAutoFillsAskOnAndResets(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	e.setMode(t, "review", "auto", "[]")
	// A pending approval at the gate is cancelled.
	task, _ := e.q.CreateTask(ctx, gen.CreateTaskParams{ID: uuid.NewString(), Title: "p", WorkflowID: e.wfID, RepoID: e.repo, Label: "review"})
	pend := "pending"
	if err := e.q.CreateAutonomyDecision(ctx, gen.CreateAutonomyDecisionParams{ID: "pend1", TaskID: task.ID, WorkflowID: e.wfID, Label: "review", Mode: "auto", Decision: "approve", Reasons: "[]", Outcome: &pend}); err != nil {
		t.Fatal(err)
	}
	e.escape(t, nil)
	ch, _ := e.escape(t, nil)
	if ch == nil || ch.FromMode != "auto" || ch.ToMode != "on_flags" || ch.Actor != "policy" || !strings.Contains(ch.Reason, "ask_on set to all flags") {
		t.Fatalf("change = %+v", ch)
	}
	m, askOn := e.mode(t, "review")
	if m != "on_flags" || askOn == "[]" {
		t.Fatalf("mode=%s askOn=%s", m, askOn)
	}
	d, _ := e.q.GetAutonomyDecision(ctx, "pend1")
	if d.Outcome == nil || *d.Outcome != "superseded" {
		t.Fatalf("pending not superseded: %+v", d.Outcome)
	}
	if e.pub.count(wsevent.TypeAutonomySteppedDown) != 1 || e.pub.count(wsevent.TypeTaskAutonomyAsked) != 1 {
		t.Fatalf("events %v", e.pub.ev)
	}
	// Counter reset: one more escape does not step again, two do.
	if ch, _ := e.escape(t, nil); ch != nil {
		t.Fatal("stepped again too early")
	}
	ch, _ = e.escape(t, nil)
	if ch == nil || ch.FromMode != "on_flags" || ch.ToMode != "ask" {
		t.Fatalf("second step = %+v", ch)
	}
	// ask: nothing more.
	e.escape(t, nil)
	if ch, _ := e.escape(t, nil); ch != nil {
		t.Fatal("stepped below ask")
	}
}

func TestSameDecisionCountsOnce(t *testing.T) {
	e := setup(t)
	e.setMode(t, "review", "auto", "[]")
	_, id := e.escape(t, nil)
	if ch, _ := e.escape(t, &id); ch != nil {
		t.Fatal("same decision counted twice")
	}
}

func TestWindowExpiry(t *testing.T) {
	e := setup(t)
	e.setMode(t, "review", "auto", "[]")
	e.escape(t, nil)
	e.now = e.now.Add(15 * 24 * time.Hour)
	if ch, _ := e.escape(t, nil); ch != nil {
		t.Fatal("old escape counted")
	}
}

func TestDisabledAndCustomSettings(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	e.setMode(t, "review", "auto", "[]")
	if _, err := e.q.UpsertAutonomySettings(ctx, gen.UpsertAutonomySettingsParams{StepDownEscapes: 0, StepDownWindowDays: 14}); err != nil {
		t.Fatal(err)
	}
	e.escape(t, nil)
	e.escape(t, nil)
	e.escape(t, nil)
	if m, _ := e.mode(t, "review"); m != "auto" {
		t.Fatal("stepped while disabled")
	}
	if _, err := e.q.UpsertAutonomySettings(ctx, gen.UpsertAutonomySettingsParams{StepDownEscapes: 3, StepDownWindowDays: 14}); err != nil {
		t.Fatal(err)
	}
	ch, _ := e.rec.CheckStepDown(ctx, e.wfID, "review")
	if ch == nil {
		t.Fatal("expected step with 3 escapes and N=3")
	}
}

func TestOnFlagsToAsk(t *testing.T) {
	e := setup(t)
	e.setMode(t, "review", "on_flags", `["migration"]`)
	e.escape(t, nil)
	ch, _ := e.escape(t, nil)
	if ch == nil || ch.ToMode != "ask" {
		t.Fatalf("%+v", ch)
	}
	if _, a := e.mode(t, "review"); a != `["migration"]` {
		t.Fatalf("ask_on changed: %s", a)
	}
}
