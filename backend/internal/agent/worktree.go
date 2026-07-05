package agent

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// worktreeDir is the subdirectory under a repo where per-task worktrees live.
const worktreeDir = ".ate-worktrees"

var slugRe = regexp.MustCompile(`[^a-z0-9]+`)

// branchName builds a concise, [a-z0-9-]-only branch name from a task's title
// and ID, e.g. "ate-fix-log-3f9a1c2b".
func branchName(taskID, title string) string {
	slug := slugRe.ReplaceAllString(strings.ToLower(title), "-")
	slug = strings.Trim(slug, "-")
	if len(slug) > 7 {
		slug = strings.Trim(slug[:7], "-")
	}
	if slug == "" {
		slug = "task"
	}
	short := taskID
	if len(short) > 8 {
		short = short[:8]
	}
	return fmt.Sprintf("ate-%s-%s", slug, short)
}

// provisionWorktree ensures a worktree + branch exist for the task and returns
// the worktree path, branch name, and the base ref it forked from. Idempotent:
// if the worktree already exists on disk it is returned as-is.
func provisionWorktree(ctx context.Context, repoPath, taskID, title string) (wtPath, branch, baseRef string, err error) {
	return provisionWorktreeFrom(ctx, repoPath, taskID, title, "")
}

// provisionWorktreeFrom is provisionWorktree with an explicit base ref. When
// baseOverride is non-empty the new branch is cut from it (used to branch a
// subtask off its parent's branch); otherwise the repo's default branch is used.
func provisionWorktreeFrom(ctx context.Context, repoPath, taskID, title, baseOverride string) (wtPath, branch, baseRef string, err error) {
	branch = branchName(taskID, title)
	wtPath = filepath.Join(repoPath, worktreeDir, taskID)

	if fi, statErr := os.Stat(wtPath); statErr == nil && fi.IsDir() {
		base := baseOverride
		if base == "" {
			base, _ = defaultBaseRef(ctx, repoPath)
		}
		return wtPath, branch, base, nil
	}

	// Fetch latest before branching so the task starts from current main.
	// Best-effort: if there's no remote or no connectivity, proceed with local state.
	_, _ = git(ctx, repoPath, "fetch", "--prune")

	if baseOverride != "" {
		baseRef = baseOverride
	} else {
		baseRef, err = defaultBaseRef(ctx, repoPath)
		if err != nil {
			return "", "", "", err
		}
	}

	// Keep the worktrees dir out of the main tree's untracked listing.
	excludeWorktreeDir(repoPath)

	// Create the worktree. If the branch already exists (e.g. a previous worktree
	// was pruned but the branch kept), add without -b.
	if out, addErr := git(ctx, repoPath, "worktree", "add", "-b", branch, wtPath, baseRef); addErr != nil {
		if !strings.Contains(string(out), "already exists") {
			return "", "", "", fmt.Errorf("git worktree add: %w: %s", addErr, strings.TrimSpace(string(out)))
		}
		if out2, addErr2 := git(ctx, repoPath, "worktree", "add", wtPath, branch); addErr2 != nil {
			return "", "", "", fmt.Errorf("git worktree add (existing branch): %w: %s", addErr2, strings.TrimSpace(string(out2)))
		}
	}
	return wtPath, branch, baseRef, nil
}

// defaultBaseRef returns the repo's default branch ref, preferring origin/HEAD,
// then origin/main, falling back to the current HEAD.
func defaultBaseRef(ctx context.Context, repoPath string) (string, error) {
	if out, err := git(ctx, repoPath, "symbolic-ref", "--short", "refs/remotes/origin/HEAD"); err == nil {
		if ref := strings.TrimSpace(string(out)); ref != "" {
			return ref, nil
		}
	}
	if _, err := git(ctx, repoPath, "rev-parse", "--verify", "origin/main"); err == nil {
		return "origin/main", nil
	}
	out, err := git(ctx, repoPath, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return "", fmt.Errorf("resolve default branch: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

// commitIfDirty stages and commits any uncommitted changes in the worktree.
// No-op (nil) when the tree is clean. This is the safety-net: agents may commit
// their own work, but anything left dirty is captured here.
func commitIfDirty(ctx context.Context, wtPath, msg, gitName, gitEmail string) error {
	out, err := git(ctx, wtPath, "status", "--porcelain")
	if err != nil {
		return fmt.Errorf("git status: %w: %s", err, strings.TrimSpace(string(out)))
	}
	if strings.TrimSpace(string(out)) == "" {
		return nil
	}
	if out, err := git(ctx, wtPath, "add", "-A"); err != nil {
		return fmt.Errorf("git add: %w: %s", err, strings.TrimSpace(string(out)))
	}
	if out, err := git(ctx, wtPath, "-c", "user.name="+gitName, "-c", "user.email="+gitEmail, "commit", "-m", msg); err != nil {
		return fmt.Errorf("git commit: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// diffAgainstBase returns the diff of the task's branch against its merge-base
// with baseRef — i.e. exactly this task's accumulated changes.
func diffAgainstBase(ctx context.Context, wtPath, baseRef, branch string) (string, error) {
	mb, err := git(ctx, wtPath, "merge-base", baseRef, branch)
	if err != nil {
		// No merge-base (e.g. base ref gone): fall back to diffing against baseRef directly.
		out, derr := git(ctx, wtPath, "diff", baseRef, branch, "--")
		if derr != nil {
			return "", fmt.Errorf("git diff: %w: %s", derr, strings.TrimSpace(string(out)))
		}
		return string(out), nil
	}
	base := strings.TrimSpace(string(mb))
	out, err := git(ctx, wtPath, "diff", base, branch, "--")
	if err != nil {
		return "", fmt.Errorf("git diff: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

// PushBranch pushes the task's branch to origin. Caller should only invoke this
// when the repo has a remote configured.
func PushBranch(ctx context.Context, wtPath, branch string) error {
	if out, err := git(ctx, wtPath, "push", "-u", "origin", branch); err != nil {
		return fmt.Errorf("git push: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// MergeBranch merges branch into the current branch of the worktree at wtPath
// as a plain merge commit (--no-ff, keeping per-child commits in history). It
// reports conflicted=true (and leaves the tree clean via `git merge --abort`)
// when the merge hits conflicts, so the caller can flag the child and hand the
// resolution to the parent's agent. A non-conflict error is returned as err.
func MergeBranch(ctx context.Context, wtPath, branch, msg, gitName, gitEmail string) (conflicted bool, files []string, err error) {
	out, mErr := git(ctx, wtPath,
		"-c", "user.name="+gitName, "-c", "user.email="+gitEmail,
		"merge", "--no-ff", "-m", msg, branch)
	if mErr == nil {
		return false, nil, nil
	}
	// A merge that leaves conflict markers reports the conflicting paths via
	// `git diff --name-only --diff-filter=U`. If there are none, the failure was
	// something else (bad ref, etc.) — surface it as a real error.
	confOut, _ := git(ctx, wtPath, "diff", "--name-only", "--diff-filter=U")
	conflicts := strings.Fields(strings.TrimSpace(string(confOut)))
	if len(conflicts) == 0 {
		return false, nil, fmt.Errorf("git merge: %w: %s", mErr, strings.TrimSpace(string(out)))
	}
	// Abort so the parent's worktree is left clean for the resolution run.
	_, _ = git(ctx, wtPath, "merge", "--abort")
	return true, conflicts, nil
}

// RemoveWorktree tears down the task's worktree directory. The branch is kept.
func RemoveWorktree(ctx context.Context, repoPath, wtPath string) error {
	if wtPath == "" {
		return nil
	}
	if out, err := git(ctx, repoPath, "worktree", "remove", "--force", wtPath); err != nil {
		return fmt.Errorf("git worktree remove: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// DeleteLocalBranch removes the task's local branch from the main clone at
// repoPath. Intended to be called once a task's PR has been confirmed merged
// on GitHub — at that point the branch's work is preserved upstream and the
// branch is no longer needed for local review. Only the local branch is
// deleted; any remote branch (e.g. on origin) is left untouched.
//
// Safe to call even if the branch doesn't exist (treated as success), so
// callers don't need to track whether cleanup already ran.
func DeleteLocalBranch(ctx context.Context, repoPath, branch string) error {
	if branch == "" {
		return nil
	}
	out, err := git(ctx, repoPath, "branch", "-D", branch)
	if err != nil {
		if strings.Contains(string(out), "not found") {
			return nil // already gone — fine
		}
		return fmt.Errorf("git branch -D: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// excludeWorktreeDir appends the worktree dir to the repo's .git/info/exclude so
// it never shows up as untracked in the main working tree. Best-effort.
func excludeWorktreeDir(repoPath string) {
	excludePath := filepath.Join(repoPath, ".git", "info", "exclude")
	data, err := os.ReadFile(excludePath)
	if err == nil && strings.Contains(string(data), worktreeDir+"/") {
		return
	}
	f, err := os.OpenFile(excludePath, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0644)
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()
	_, _ = f.WriteString("\n" + worktreeDir + "/\n")
}

// git runs a git command in dir and returns combined output.
func git(ctx context.Context, dir string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	return cmd.CombinedOutput()
}
