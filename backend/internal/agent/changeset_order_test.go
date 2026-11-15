package agent

import (
	"reflect"
	"testing"
	"time"

	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

func TestChangeSetMergeOrder(t *testing.T) {
	t0 := time.Unix(1000, 0)
	mk := func(id string, off int) gen.Task {
		return gen.Task{ID: id, CreatedAt: t0.Add(time.Duration(off) * time.Second)}
	}
	kids := []gen.Task{mk("c", 3), mk("a", 1), mk("b", 2)}
	edge := func(task, dep string) gen.ListWorkflowDependencyEdgesRow {
		return gen.ListWorkflowDependencyEdgesRow{TaskID: task, DependsOnTaskID: dep}
	}
	if _, ok := ChangeSetMergeOrder(kids, nil); ok {
		t.Fatal("no edges must give no order")
	}
	if _, ok := ChangeSetMergeOrder(kids, []gen.ListWorkflowDependencyEdgesRow{edge("a", "parent"), edge("b", "parent")}); ok {
		t.Fatal("parent edges must be ignored")
	}
	got, ok := ChangeSetMergeOrder(kids, []gen.ListWorkflowDependencyEdgesRow{edge("b", "a"), edge("x", "b")})
	if !ok || !reflect.DeepEqual(got, []string{"a", "b", "c"}) {
		t.Fatalf("chain a->b: %v %v", got, ok)
	}
	// c must come before a; ties (b) stable by created_at.
	got, ok = ChangeSetMergeOrder(kids, []gen.ListWorkflowDependencyEdgesRow{edge("a", "c")})
	if !ok || !reflect.DeepEqual(got, []string{"b", "c", "a"}) {
		t.Fatalf("c->a: %v", got)
	}
	if _, ok := ChangeSetMergeOrder(kids, []gen.ListWorkflowDependencyEdgesRow{edge("b", "a"), edge("a", "b")}); ok {
		t.Fatal("cycle must give no order")
	}
}
