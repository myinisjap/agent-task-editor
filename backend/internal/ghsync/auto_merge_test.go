package ghsync

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/myinisjap/agent-task-editor/backend/internal/automerge"
	"github.com/myinisjap/agent-task-editor/backend/internal/forge"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

func TestSyncTask_AutoMerge(t *testing.T) {
	ctx := context.Background()
	head := &forge.PRHead{Number: 1, HeadSHA: "sha1", BaseRef: "main", Mergeable: forge.MergeableClean, State: "pr_open", URL: "https://github.com/acme/widgets/pull/1"}
	checks := []forge.Check{{Name: "ci", Bucket: "pending"}}

	s, q, _ := newTestSyncerFull(t,
		func(context.Context, repoInfo, string) (forge.PRHead, error) { return *head, nil },
		func(context.Context, repoInfo, int) ([]forge.Review, error) { return nil, nil },
		func(context.Context, repoInfo, int) ([]forge.PRReviewComment, error) { return nil, nil },
		func(context.Context, repoInfo, int) ([]forge.Check, error) { return nil, nil },
		true)
	s.getChecks = func(context.Context, repoInfo, int) ([]forge.Check, error) { return checks, nil }

	wfID, label := mustCreateSimpleWorkflow(t, q)
	repoID := uuid.NewString()
	if _, err := q.CreateRepo(ctx, gen.CreateRepoParams{
		ID: repoID, Name: "widgets", Path: t.TempDir(), RemoteUrl: ghURL(), WorkflowID: &wfID,
		AutoMergeMode: "when_green", AutoMergeMethod: "squash",
	}); err != nil {
		t.Fatal(err)
	}
	task := newTestTask(t, q, repoID, wfID, label, "feature-branch", "", "pr_open", "https://github.com/acme/widgets/pull/1")
	ri := repoInfo{ghName: "acme/widgets", repo: mustGetRepo(t, q, repoID)}

	count := func() int64 {
		n, err := q.CountTaskSideEffectsOfKind(ctx, gen.CountTaskSideEffectsOfKindParams{TaskID: task.ID, Kind: "auto_merge"})
		if err != nil {
			t.Fatal(err)
		}
		return n
	}

	// Pending check: blocked, state recorded for the UI, nothing queued.
	s.syncTask(ctx, task, ri)
	row, err := q.GetTaskAutoMerge(ctx, task.ID)
	if err != nil || row.State != automerge.StateWaiting || row.Reason != automerge.ReasonChecksPending {
		t.Fatalf("row=%+v err=%v", row, err)
	}
	if count() != 0 {
		t.Fatal("must not queue a merge while a check is pending")
	}

	// Checks go green on the next refresh: merge queued exactly once.
	checks = []forge.Check{{Name: "ci", Bucket: "pass"}}
	s.syncTask(ctx, task, ri)
	s.syncTask(ctx, task, ri)
	if count() != 1 {
		t.Fatalf("want one queued merge, got %d", count())
	}
	if row, _ = q.GetTaskAutoMerge(ctx, task.ID); row.State != automerge.StateMerging {
		t.Fatalf("row=%+v", row)
	}

	// Policy off: verdict cleared.
	ri.repo.AutoMergeMode = "off"
	s.syncTask(ctx, task, ri)
	if _, err := q.GetTaskAutoMerge(ctx, task.ID); err == nil {
		t.Fatal("verdict should be cleared when mode is off")
	}
}
