package intakehook

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/myinisjap/agent-task-editor/backend/internal/intake"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
	"github.com/myinisjap/agent-task-editor/backend/internal/wsevent"
)

type pub struct {
	mu     sync.Mutex
	events []string
}

func (p *pub) Publish(t string, _ any) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.events = append(p.events, t)
}

type env struct {
	q    *gen.Queries
	svc  *Service
	pub  *pub
	wf   gen.Workflow
	repo gen.Repo
}

func setup(t *testing.T, withWorkflow bool) *env {
	t.Helper()
	f, err := os.CreateTemp("", "intakehook-*.db")
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	t.Cleanup(func() { _ = os.Remove(f.Name()) })
	db, err := storage.Open(f.Name())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	q := gen.New(db.SQL())
	ctx := context.Background()
	wf, err := q.CreateWorkflow(ctx, gen.CreateWorkflowParams{ID: uuid.NewString(), Name: "wf-" + uuid.NewString()})
	if err != nil {
		t.Fatal(err)
	}
	for i, l := range []gen.CreateWorkflowLabelParams{
		{Name: "not_ready", AgentIgnore: 1},
		{Name: "todo"},
		{Name: "done", IsTerminal: 1},
	} {
		l.ID = uuid.NewString()
		l.WorkflowID = wf.ID
		l.Color = "#888"
		l.SortOrder = int64(i)
		if _, err := q.CreateWorkflowLabel(ctx, l); err != nil {
			t.Fatal(err)
		}
	}
	var wfID *string
	if withWorkflow {
		wfID = &wf.ID
	}
	repo, err := q.CreateRepo(ctx, gen.CreateRepoParams{ID: uuid.NewString(), Name: "r", Path: t.TempDir(), WorkflowID: wfID})
	if err != nil {
		t.Fatal(err)
	}
	p := &pub{}
	return &env{q: q, svc: NewService(db.SQL(), q, p), pub: p, wf: wf, repo: repo}
}

func (e *env) endpoint(t *testing.T, mod func(*gen.CreateIntakeEndpointParams)) gen.IntakeEndpoint {
	t.Helper()
	p := gen.CreateIntakeEndpointParams{
		ID: uuid.NewString(), Name: "ci", RepoID: e.repo.ID, Secret: "s", Enabled: true,
		TitleTemplate: "{{alert.title}}", DedupeKeyTemplate: "{{alert.id}}",
	}
	if mod != nil {
		mod(&p)
	}
	ep, err := e.q.CreateIntakeEndpoint(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	return ep
}

func payload(t *testing.T, s string) any {
	t.Helper()
	v, err := intake.ParsePayload([]byte(s))
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func (e *env) rule(t *testing.T, mod func(*gen.CreateIntakeRuleParams)) gen.IntakeRule {
	t.Helper()
	p := gen.CreateIntakeRuleParams{ID: uuid.NewString(), Name: "r", Enabled: true, MatchSource: "webhook", MatchLabels: "[]", MatchAuthorAssoc: "[]"}
	mod(&p)
	r, err := e.q.CreateIntakeRule(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestReceive_CreatesOnGate(t *testing.T) {
	e := setup(t, true)
	ep := e.endpoint(t, nil)
	res, err := e.svc.Receive(context.Background(), ep, payload(t, `{"alert":{"title":"CI red","id":"1"}}`))
	if err != nil || !res.Created {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	task, _ := e.q.GetTask(context.Background(), res.TaskID)
	if task.Label != "not_ready" || task.Source != "webhook" || task.Title != "CI red" || task.Type != "feature" {
		t.Errorf("task = %+v", task)
	}
	got, _ := e.q.GetIntakeEndpoint(context.Background(), ep.ID)
	if got.LastReceivedAt == nil {
		t.Error("last_received_at not set")
	}
	if len(e.pub.events) != 1 || e.pub.events[0] != wsevent.TypeTaskCreated {
		t.Errorf("events = %v", e.pub.events)
	}
}

func TestShape_EndpointTargetLabelWins(t *testing.T) {
	e := setup(t, true)
	ep := e.endpoint(t, func(p *gen.CreateIntakeEndpointParams) { p.TargetLabel = "todo" })
	sh, err := e.svc.Shape(context.Background(), ep, payload(t, `{"alert":{"title":"x"}}`))
	if err != nil || sh.Label != "todo" {
		t.Fatalf("sh=%+v err=%v", sh, err)
	}
}

func TestShape_RuleAppliesPriorityCost(t *testing.T) {
	e := setup(t, true)
	ep := e.endpoint(t, nil)
	prio, cost := int64(2), 3.0
	r := e.rule(t, func(p *gen.CreateIntakeRuleParams) { p.ApplyPriority = &prio; p.ApplyMaxCostUsd = &cost })
	sh, err := e.svc.Shape(context.Background(), ep, payload(t, `{"alert":{"title":"x"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if sh.Priority != 2 || sh.MaxCostUsd != 3.0 || sh.MatchedRuleID == nil || *sh.MatchedRuleID != r.ID {
		t.Errorf("sh = %+v", sh)
	}
}

func TestShape_RuleNonGateTargetFailsClosed(t *testing.T) {
	e := setup(t, true)
	ep := e.endpoint(t, nil)
	e.rule(t, func(p *gen.CreateIntakeRuleParams) { p.ApplyTargetLabel = "todo" })
	sh, err := e.svc.Shape(context.Background(), ep, payload(t, `{"alert":{"title":"x"}}`))
	if err != nil || sh.Label != "not_ready" {
		t.Fatalf("sh=%+v err=%v", sh, err)
	}
}

func TestShape_RuleGateTargetAndTemplate(t *testing.T) {
	e := setup(t, true)
	ep := e.endpoint(t, nil)
	tmpl, err := e.q.CreateTaskTemplate(context.Background(), gen.CreateTaskTemplateParams{ID: uuid.NewString(), Name: "t", Title: "t", Description: "Do it", Type: "bug"})
	if err != nil {
		t.Fatal(err)
	}
	e.rule(t, func(p *gen.CreateIntakeRuleParams) { p.ApplyTargetLabel = "not_ready"; p.ApplyTemplateID = &tmpl.ID })
	sh, err := e.svc.Shape(context.Background(), ep, payload(t, `{"alert":{"title":"x"}}`))
	if err != nil || sh.Type != "bug" || sh.Label != "not_ready" || sh.Description[:5] != "Do it" {
		t.Fatalf("sh=%+v err=%v", sh, err)
	}
}

func TestReceive_Dedupe(t *testing.T) {
	e := setup(t, true)
	ctx := context.Background()
	ep := e.endpoint(t, nil)
	first, err := e.svc.Receive(ctx, ep, payload(t, `{"alert":{"title":"a","id":"1"}}`))
	if err != nil {
		t.Fatal(err)
	}
	second, err := e.svc.Receive(ctx, ep, payload(t, `{"alert":{"title":"a again","id":"1"}}`))
	if err != nil || !second.Deduped || second.TaskID != first.TaskID {
		t.Fatalf("second=%+v err=%v", second, err)
	}
	cs, _ := e.q.ListTaskSourceComments(ctx, first.TaskID)
	if len(cs) != 1 || cs[0].Author != "intake:ci" {
		t.Errorf("comments = %+v", cs)
	}
	// different key -> new task
	other, err := e.svc.Receive(ctx, ep, payload(t, `{"alert":{"title":"b","id":"2"}}`))
	if err != nil || !other.Created || other.TaskID == first.TaskID {
		t.Fatalf("other=%+v err=%v", other, err)
	}

	// terminal task -> new task
	if _, err := e.q.UpdateTaskLabel(ctx, gen.UpdateTaskLabelParams{Label: "done", ID: first.TaskID}); err != nil {
		t.Fatal(err)
	}
	third, err := e.svc.Receive(ctx, ep, payload(t, `{"alert":{"title":"a","id":"1"}}`))
	if err != nil || !third.Created {
		t.Fatalf("third=%+v err=%v", third, err)
	}
	// archived -> new task
	if _, err := e.q.SetTaskArchived(ctx, gen.SetTaskArchivedParams{Archived: 1, ID: third.TaskID}); err != nil {
		t.Fatal(err)
	}
	fourth, err := e.svc.Receive(ctx, ep, payload(t, `{"alert":{"title":"a","id":"1"}}`))
	if err != nil || !fourth.Created {
		t.Fatalf("fourth=%+v err=%v", fourth, err)
	}
}

func TestReceive_NoDedupeKeyAlwaysCreates(t *testing.T) {
	e := setup(t, true)
	ep := e.endpoint(t, func(p *gen.CreateIntakeEndpointParams) { p.DedupeKeyTemplate = "" })
	for i := 0; i < 2; i++ {
		res, err := e.svc.Receive(context.Background(), ep, payload(t, `{"alert":{"title":"a"}}`))
		if err != nil || !res.Created {
			t.Fatalf("res=%+v err=%v", res, err)
		}
	}
}

func TestReceive_NoWorkflow(t *testing.T) {
	e := setup(t, false)
	ep := e.endpoint(t, nil)
	_, err := e.svc.Receive(context.Background(), ep, payload(t, `{}`))
	if !errors.Is(err, ErrNoWorkflow) {
		t.Fatalf("err = %v", err)
	}
	// endpoint workflow rescues it
	ep = e.endpoint(t, func(p *gen.CreateIntakeEndpointParams) { p.WorkflowID = &e.wf.ID })
	if _, err := e.svc.Receive(context.Background(), ep, payload(t, `{}`)); err != nil {
		t.Fatal(err)
	}
}
