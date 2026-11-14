package storage

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

func TestPickDefaultWorkflow(t *testing.T) {
	w := func(id, name string) gen.Workflow { return gen.Workflow{ID: id, Name: name} }
	cases := []struct {
		name string
		in   []gen.Workflow
		want string
		ok   bool
	}{
		{"default first", []gen.Workflow{w("b", "Default"), w("a", PRReviewWorkflowName)}, "b", true},
		{"pr first", []gen.Workflow{w("a", PRReviewWorkflowName), w("b", "Default")}, "b", true},
		{"renamed", []gen.Workflow{w("a", PRReviewWorkflowName), w("z", "Zeta")}, "z", true},
		{"only pr", []gen.Workflow{w("a", PRReviewWorkflowName)}, "a", true},
		{"alpha then id", []gen.Workflow{w("3", "B"), w("2", "A"), w("1", "A")}, "1", true},
		{"empty", nil, "", false},
	}
	for _, c := range cases {
		got, ok := PickDefaultWorkflow(c.in)
		if ok != c.ok || got.ID != c.want {
			t.Errorf("%s: got %q,%v want %q,%v", c.name, got.ID, ok, c.want, c.ok)
		}
	}
}

func TestDefaultWorkflowID_DB(t *testing.T) {
	for _, reverse := range []bool{false, true} {
		db, err := Open(t.TempDir() + "/d.db")
		if err != nil {
			t.Fatal(err)
		}
		ctx := context.Background()
		q := gen.New(db.SQL())
		var want string
		if !reverse {
			if err := SeedDefaultWorkflow(ctx, db); err != nil {
				t.Fatal(err)
			}
			if err := SeedPRReviewWorkflow(ctx, db); err != nil {
				t.Fatal(err)
			}
			wf, err := q.GetWorkflowByName(ctx, "Default")
			if err != nil {
				t.Fatal(err)
			}
			want = wf.ID
		} else {
			if err := SeedPRReviewWorkflow(ctx, db); err != nil {
				t.Fatal(err)
			}
			wf, err := q.CreateWorkflow(ctx, gen.CreateWorkflowParams{ID: uuid.NewString(), Name: "Default"})
			if err != nil {
				t.Fatal(err)
			}
			want = wf.ID
		}
		if _, err := db.SQL().Exec(`UPDATE workflows SET created_at = '2024-01-01 00:00:00'`); err != nil {
			t.Fatal(err)
		}
		got, err := DefaultWorkflowID(ctx, q)
		if err != nil || got != want {
			t.Errorf("reverse=%v got %q,%v want %q", reverse, got, err, want)
		}
		_ = db.Close()
	}
}
