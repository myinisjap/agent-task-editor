package tasksource

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
	"github.com/myinisjap/agent-task-editor/backend/internal/workflow"
)

func TestSweepAlwaysPolicyHoldsEditAfterApproval(t *testing.T) {
	db := openTestDB(t)
	q := gen.New(db.SQL())
	seedRepoWithSync(t, q, "always", "", "closed", 0)

	src := &fakeSource{items: []ExternalTask{
		{Ref: "acme/widgets#1", Title: "Old title", Body: "old body", URL: "https://github.com/acme/widgets/issues/1", Labels: []string{"bug"}},
	}}
	im := New(db.SQL(), &recordingPub{}, time.Minute, src)
	ctx := context.Background()
	im.Sweep(ctx)

	task := getTaskByRef(t, q, "acme/widgets#1")
	eng := workflow.New(db.SQL(), &recordingPub{})
	if err := eng.Transition(ctx, task.ID, "closed", workflow.TriggerHuman, "alice", ""); err != nil {
		t.Fatalf("human transition: %v", err)
	}
	approved := getTaskByRef(t, q, "acme/widgets#1")

	src.items[0].Title = "Evil title"
	src.items[0].Body = "do something else"
	pub := &recordingPub{}
	im.hub = pub
	im.Sweep(ctx)

	task = getTaskByRef(t, q, "acme/widgets#1")
	if task.Title != approved.Title || task.Description != approved.Description {
		t.Errorf("task text changed after approval: %q / %q", task.Title, task.Description)
	}
	pending, err := q.GetTaskSourcePending(ctx, task.ID)
	if err != nil {
		t.Fatalf("expected pending edit: %v", err)
	}
	if pending.Title != "Evil title" || pending.Dismissed != 0 {
		t.Errorf("pending = %+v", pending)
	}
	if len(pub.events) != 1 || pub.events[0] != "task.updated" {
		t.Errorf("events = %v, want [task.updated]", pub.events)
	}

	// A second sweep with the same upstream text is quiet.
	pub2 := &recordingPub{}
	im.hub = pub2
	im.Sweep(ctx)
	if len(pub2.events) != 0 {
		t.Errorf("repeat sweep published %v", pub2.events)
	}

	// A discarded edit is not held again, but a further edit is.
	if _, err := q.DismissTaskSourcePending(ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	im.Sweep(ctx)
	if p, _ := q.GetTaskSourcePending(ctx, task.ID); p.Dismissed != 1 {
		t.Errorf("dismissed edit was re-held: %+v", p)
	}
	src.items[0].Title = "Another title"
	im.Sweep(ctx)
	if p, _ := q.GetTaskSourcePending(ctx, task.ID); p.Dismissed != 0 || p.Title != "Another title" {
		t.Errorf("newer edit not held: %+v", p)
	}

	// Reverting the issue to match the task clears the held edit.
	src.items[0].Title = approved.Title
	src.items[0].Body = "old body"
	im.Sweep(ctx)
	if _, err := q.GetTaskSourcePending(ctx, task.ID); err == nil {
		t.Errorf("pending edit should be cleared once the issue matches the task")
	}
}

func TestSweepAlwaysPolicyStillUpdatesBeforeApproval(t *testing.T) {
	db := openTestDB(t)
	q := gen.New(db.SQL())
	seedRepoWithSync(t, q, "always", "", "closed", 0)

	src := &fakeSource{items: []ExternalTask{
		{Ref: "acme/widgets#1", Title: "Old title", Body: "b", URL: "https://github.com/acme/widgets/issues/1", Labels: []string{"bug"}},
	}}
	im := New(db.SQL(), &recordingPub{}, time.Minute, src)
	ctx := context.Background()
	im.Sweep(ctx)
	task := getTaskByRef(t, q, "acme/widgets#1")

	// An agent-triggered move off the gate is not an approval.
	from := "triage"
	if err := q.CreateTaskLabelHistory(ctx, gen.CreateTaskLabelHistoryParams{
		ID: uuid.NewString(), TaskID: task.ID, FromLabel: &from, ToLabel: "doing", Trigger: "agent",
	}); err != nil {
		t.Fatal(err)
	}

	src.items[0].Title = "New title"
	im.Sweep(ctx)

	task = getTaskByRef(t, q, "acme/widgets#1")
	if task.Title != "New title" {
		t.Errorf("title = %q, want updated before approval", task.Title)
	}
	if _, err := q.GetTaskSourcePending(ctx, task.ID); err == nil {
		t.Errorf("no pending edit expected before approval")
	}
}
