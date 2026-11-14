package agent

import (
	"context"
	"testing"

	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

type fakeRepoMap struct{ gotRepo, gotLabel, gotAgent string }

func (f *fakeRepoMap) PromptSection(_ context.Context, repoID, label, agentID string) string {
	f.gotRepo, f.gotLabel, f.gotAgent = repoID, label, agentID
	return "SECTION"
}

func TestRepoMapSectionWiring(t *testing.T) {
	d := &Dispatcher{}
	task := gen.Task{RepoID: "r1", Label: "plan"}
	if got := d.repoMapSection(context.Background(), task, "a1"); got != "" {
		t.Fatalf("nil source must give no section, got %q", got)
	}
	f := &fakeRepoMap{}
	d.RepoMap = f
	if got := d.repoMapSection(context.Background(), task, "a1"); got != "SECTION" {
		t.Fatalf("got %q", got)
	}
	if f.gotRepo != "r1" || f.gotLabel != "plan" || f.gotAgent != "a1" {
		t.Fatalf("args = %+v", f)
	}
}
