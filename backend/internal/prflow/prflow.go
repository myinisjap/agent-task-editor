// Package prflow owns "push a task's branch and create (or find) its pull
// request": forge resolution, the push, PR body assembly, persisting the
// resulting PR URL + git state on the task, and the opt-in issue write-back.
//
// It is shared by the HTTP CreatePR handler and the workflow engine's
// OnCreatePR side-effect hook (wired in cmd/server) so both produce an
// identical PR. It deliberately does not import api/handlers or workflow.
package prflow

import (
	"context"
	"errors"
	"fmt"
	"github.com/myinisjap/agent-task-editor/backend/internal/gitcmd"
	"strings"

	"github.com/myinisjap/agent-task-editor/backend/internal/agent"
	"github.com/myinisjap/agent-task-editor/backend/internal/forge"
	_ "github.com/myinisjap/agent-task-editor/backend/internal/ghclient" // registers the GitHub forge.Forge implementation
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
	"github.com/myinisjap/agent-task-editor/backend/internal/writeback"
)

// Sentinel errors tag PR-creation failures so HTTP callers can map them to a
// status code while non-HTTP callers (the workflow OnCreatePR hook) can just
// log or retry them.
var (
	// ErrPrecondition marks a task or repo that cannot have a PR at all (no
	// branch, no remote, unsupported forge).
	ErrPrecondition = errors.New("bad request")
	// ErrUpstream marks a failure reported by the forge when creating the PR.
	ErrUpstream = errors.New("bad gateway")
)

// IsPrecondition reports whether err from CreateForTask means the task or repo
// cannot have a PR at all (no branch, no remote, unsupported forge) as opposed
// to a transient failure worth retrying.
func IsPrecondition(err error) bool { return errors.Is(err, ErrPrecondition) }

// IsUpstream reports whether err from CreateForTask is a forge-side failure to
// create the PR (as opposed to a precondition or local failure).
func IsUpstream(err error) bool { return errors.Is(err, ErrUpstream) }

// Store is the slice of the task store prflow needs. *gen.Queries satisfies it.
type Store interface {
	SetTaskPR(ctx context.Context, arg gen.SetTaskPRParams) (gen.Task, error)
}

// Service creates PRs for tasks.
type Service struct {
	store Store
	wb    *writeback.Writeback
}

// New returns a Service persisting PR state through store. wb may be nil to
// skip the issue write-back.
func New(store Store, wb *writeback.Writeback) *Service {
	return &Service{store: store, wb: wb}
}

// CreateForTask pushes the task's branch to origin and opens (or reuses) a
// pull request on the repo's forge, persists the resulting PR URL + git state
// on the task, and runs the issue write-back. It is idempotent: an existing PR
// for the branch is returned rather than erroring.
func (s *Service) CreateForTask(ctx context.Context, task gen.Task, repo gen.Repo) (updated gen.Task, prURL string, err error) {
	if task.Branch == "" {
		return gen.Task{}, "", fmt.Errorf("%w: task has no branch yet", ErrPrecondition)
	}
	if repo.RemoteUrl == nil {
		return gen.Task{}, "", fmt.Errorf("%w: repo has no remote_url", ErrPrecondition)
	}
	f, ghName, ok := forge.ForRemote(*repo.RemoteUrl)
	if !ok {
		return gen.Task{}, "", fmt.Errorf("%w: repo remote is not a supported forge URL", ErrPrecondition)
	}

	// Push the branch first. Push from the worktree if it still exists,
	// otherwise from the main clone — the branch ref lives there too once the
	// worktree has been torn down on a terminal transition.
	gitDir := GitDir(task, repo)
	if err := agent.PushBranch(ctx, gitDir, task.Branch); err != nil {
		return gen.Task{}, "", fmt.Errorf("failed to push branch: %w", err)
	}

	// The compare/PR base wants a branch name, not a remote-tracking ref.
	base := strings.TrimPrefix(task.BaseRef, "origin/")
	body := BuildBody(task, BranchCommits(ctx, gitDir, task.BaseRef, task.Branch))

	state, prURL, err := f.CreatePR(ctx, ghName, task.Branch, base, task.Title, body)
	if err != nil {
		return gen.Task{}, "", fmt.Errorf("%w: %s", ErrUpstream, err.Error())
	}

	updated, err = s.store.SetTaskPR(ctx, gen.SetTaskPRParams{
		GitState: state,
		PrUrl:    prURL,
		ID:       task.ID,
	})
	if err != nil {
		return gen.Task{}, "", err
	}

	// Status write-back to the source GitHub issue (opt-in per repo, no-op if
	// the task wasn't imported or the repo doesn't have it enabled).
	if s.wb != nil {
		s.wb.OnPROpened(ctx, updated, repo)
		s.wb.OnPRMerged(ctx, updated, repo)
	}

	return updated, prURL, nil
}

// GitDir returns the directory git commands about the task's branch should run
// in: its worktree if that still exists, otherwise the repo's main clone (which
// keeps the branch ref after the worktree is torn down on a terminal label).
func GitDir(task gen.Task, repo gen.Repo) string {
	if task.WorktreePath == "" || !gitcmd.DirExists(task.WorktreePath) {
		return repo.Path
	}
	return task.WorktreePath
}

// BranchCommits returns the commit subjects unique to the task's branch
// relative to its base ref (best-effort — empty slice if the git log fails or
// either ref is invalid).
func BranchCommits(ctx context.Context, gitDir, baseRef, branch string) []string {
	if !gitcmd.IsValidRef(baseRef) || !gitcmd.IsValidRef(branch) {
		return nil
	}
	out, err := gitcmd.Output(ctx, gitDir, "log", "--format=%s", baseRef+".."+branch)
	if err != nil {
		return nil
	}
	var commits []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line != "" {
			commits = append(commits, line)
		}
	}
	return commits
}

// BuildBody assembles a markdown PR description from the task and its commits.
func BuildBody(task gen.Task, commits []string) string {
	var b strings.Builder
	if task.Description != "" {
		b.WriteString(task.Description)
		b.WriteString("\n\n")
	}
	if task.AgentNotes != "" {
		b.WriteString("### What changed\n\n")
		b.WriteString(task.AgentNotes)
		b.WriteString("\n\n")
	}
	if len(commits) > 0 {
		b.WriteString("### Commits\n\n")
		for _, c := range commits {
			b.WriteString("- ")
			b.WriteString(c)
			b.WriteString("\n")
		}
	}
	return strings.TrimSpace(b.String())
}
