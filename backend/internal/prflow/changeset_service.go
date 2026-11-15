package prflow

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"strings"

	"github.com/myinisjap/agent-task-editor/backend/internal/agent"
	"github.com/myinisjap/agent-task-editor/backend/internal/forge"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

// ChangeSetOptions configures the multi-repo PR cross-linking (ADR 0041 stage 5).
type ChangeSetOptions struct {
	// BoardBaseURL (cfg.NotifyBaseURL) builds the board link; "" = no link.
	BoardBaseURL string
	// Enqueue schedules a change_set_links outbox effect for a task; nil = never
	// update sibling PRs.
	Enqueue func(ctx context.Context, taskID string) error
}

// WithChangeSet configures change-set behaviour and returns s.
func (s *Service) WithChangeSet(o ChangeSetOptions) *Service {
	s.cs = o
	return s
}

// Body is BuildBody plus the change-set section when the task belongs to one.
func (s *Service) Body(ctx context.Context, task gen.Task, commits []string) string {
	return s.bodyFor(ctx, task, commits)
}

// bodyFor never fails: a resolution error only drops the section.
func (s *Service) bodyFor(ctx context.Context, task gen.Task, commits []string) string {
	body := BuildBody(task, commits)
	view, _, ok, err := s.resolveChangeSet(ctx, task)
	if err != nil {
		slog.Warn("prflow: resolve change set", "task_id", task.ID, "err", err)
		return body
	}
	if !ok {
		return body
	}
	return body + "\n\n" + RenderSection(view)
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// resolveChangeSet applies the membership rules (DB only, no forge calls).
// ok=false means the task is not in a change set.
func (s *Service) resolveChangeSet(ctx context.Context, task gen.Task) (view ChangeSetView, members []gen.Task, ok bool, err error) {
	var parent gen.Task
	if task.ParentTaskID != nil && *task.ParentTaskID != "" {
		parent, err = s.store.GetTask(ctx, *task.ParentTaskID)
		if err != nil {
			return view, nil, false, err
		}
	} else {
		parent = task
	}
	kids, err := s.store.ListSubtasks(ctx, &parent.ID)
	if err != nil {
		return view, nil, false, err
	}
	var children []gen.Task
	for i := len(kids) - 1; i >= 0; i-- { // ListSubtasks is newest first
		if kids[i].Archived == 0 {
			children = append(children, kids[i])
		}
	}
	if !slices.ContainsFunc(children, func(c gen.Task) bool { return agent.IsForeignChild(parent, c) }) {
		return view, nil, false, nil
	}
	if task.ID != parent.ID && !slices.ContainsFunc(children, func(c gen.Task) bool { return c.ID == task.ID }) {
		return view, nil, false, nil
	}

	names := map[string]string{}
	repoName := func(id string) string {
		if n, ok := names[id]; ok {
			return n
		}
		n := shortID(id)
		if r, rerr := s.store.GetRepo(ctx, id); rerr == nil && r.Name != "" {
			n = r.Name
		}
		names[id] = n
		return n
	}
	view = ChangeSetView{ParentTitle: parent.Title, ParentShortID: shortID(parent.ID)}
	if base := strings.TrimRight(strings.TrimSpace(s.cs.BoardBaseURL), "/"); base != "" {
		view.ParentLink = base + "/tasks/" + parent.ID
	}
	members = append(members, parent)
	view.Members = append(view.Members, ChangeSetMember{TaskID: parent.ID, Title: parent.Title, RepoName: repoName(parent.RepoID), PRURL: parent.PrUrl, IsParent: true, IsSelf: parent.ID == task.ID})
	for _, c := range children {
		members = append(members, c)
		view.Members = append(view.Members, ChangeSetMember{TaskID: c.ID, Title: c.Title, RepoName: repoName(c.RepoID), PRURL: c.PrUrl, IsSelf: c.ID == task.ID})
	}
	if edges, eerr := s.store.ListWorkflowDependencyEdges(ctx, parent.WorkflowID); eerr == nil {
		if order, has := agent.ChangeSetMergeOrder(children, edges); has {
			view.MergeOrder = order
		}
	}
	return view, members, true, nil
}

// afterPROpened enqueues a change_set_links effect for every member with a PR
// (including the task itself: CreatePR is idempotent and may have returned an
// existing PR whose body predates the section). Errors are only logged.
func (s *Service) afterPROpened(ctx context.Context, task gen.Task) {
	if s.cs.Enqueue == nil {
		return
	}
	_, members, ok, err := s.resolveChangeSet(ctx, task)
	if err != nil {
		slog.Warn("prflow: resolve change set after PR opened", "task_id", task.ID, "err", err)
		return
	}
	if !ok {
		return
	}
	for _, m := range members {
		if m.PrUrl == "" {
			continue
		}
		if err := s.cs.Enqueue(ctx, m.ID); err != nil {
			slog.Warn("prflow: enqueue change_set_links", "task_id", m.ID, "err", err)
		}
	}
}

// SyncChangeSetLinks is the change_set_links outbox effect: refresh the
// change-set section of the task's PR body, or (when the forge refuses the
// edit or the body markers are malformed) post one marker-deduped comment.
// Idempotent: an unchanged body is a no-op, so retries never double-post.
// Transient errors are returned so the outbox retries.
//
// Known limit: GetPR -> EditPRBody is a read-modify-write; a human editing the
// body inside that small window could lose their edit.
func (s *Service) SyncChangeSetLinks(ctx context.Context, task gen.Task) error {
	if task.PrUrl == "" {
		return nil
	}
	cur, err := s.store.GetTask(ctx, task.ID)
	if err != nil {
		return err
	}
	task = cur
	if task.PrUrl == "" {
		return nil
	}
	view, _, ok, err := s.resolveChangeSet(ctx, task)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	repo, err := s.store.GetRepo(ctx, task.RepoID)
	if err != nil || repo.RemoteUrl == nil {
		return nil
	}
	f, name, ok := s.forgeFor(*repo.RemoteUrl)
	if !ok {
		return nil
	}
	n, ok := prNumberFromURL(task.PrUrl)
	if !ok {
		if task.Branch == "" {
			return nil
		}
		_, _, num, perr := f.PRForBranch(ctx, name, task.Branch)
		if perr != nil {
			return perr
		}
		if num <= 0 {
			return nil
		}
		n = num
	}
	pr, err := f.GetPR(ctx, name, n)
	if err != nil {
		return err
	}
	section := RenderSection(view)
	newBody, spliced := SpliceSection(pr.Body, section)
	if spliced {
		if newBody == pr.Body {
			return nil
		}
		err := f.EditPRBody(ctx, name, n, newBody)
		if err == nil {
			return nil
		}
		if !errors.Is(err, forge.ErrPRBodyEditRefused) {
			return err
		}
		slog.Info("prflow: forge refused PR body edit; commenting instead", "task_id", task.ID, "err", err)
	}
	return s.commentOnce(ctx, f, name, n, section)
}

// commentOnce posts the section as a comment unless one carrying CommentMarker
// already exists (the comment is not updated later; the board link is current).
func (s *Service) commentOnce(ctx context.Context, f forge.Forge, name string, n int, section string) error {
	comments, err := f.GetIssueComments(ctx, name, n)
	if err != nil {
		return err
	}
	for _, c := range comments {
		if strings.Contains(c.Body, CommentMarker) {
			return nil
		}
	}
	return f.CommentOnIssue(ctx, name, n, section+"\n\n"+CommentMarker)
}
