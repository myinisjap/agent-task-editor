package repomap

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/myinisjap/agent-task-editor/backend/internal/agent"
	"github.com/myinisjap/agent-task-editor/backend/internal/gitcmd"
)

// BranchInfo is the default branch's current tip.
type BranchInfo struct {
	Ref string
	SHA string
}

// DefaultBranch resolves the repo's default branch ref and its commit SHA.
func DefaultBranch(ctx context.Context, repoPath string) (BranchInfo, error) {
	ref, err := agent.DefaultBaseRef(ctx, repoPath)
	if err != nil {
		return BranchInfo{}, err
	}
	if !gitcmd.IsValidRef(ref) {
		return BranchInfo{}, fmt.Errorf("unsafe default branch ref %q", ref)
	}
	out, err := gitcmd.Output(ctx, repoPath, "rev-parse", "--verify", ref+"^{commit}")
	if err != nil {
		return BranchInfo{}, fmt.Errorf("resolve %s: %w", ref, err)
	}
	return BranchInfo{Ref: ref, SHA: strings.TrimSpace(string(out))}, nil
}

// CommitsBehind counts commits on ref that are not in sha. -1 means unknown
// (sha is not a commit of this repo any more).
func CommitsBehind(ctx context.Context, repoPath, sha, ref string) int {
	if !gitcmd.IsValidRef(sha) || !gitcmd.IsValidRef(ref) {
		return -1
	}
	out, err := gitcmd.Output(ctx, repoPath, "rev-list", "--count", sha+".."+ref)
	if err != nil {
		return -1
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		return -1
	}
	return n
}

// topLevel lists the top-level entry names of a commit's tree.
func topLevel(ctx context.Context, repoPath, rev string) ([]string, error) {
	out, err := gitcmd.Output(ctx, repoPath, "ls-tree", "--name-only", rev)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, l := range strings.Split(string(out), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			names = append(names, l)
		}
	}
	sort.Strings(names)
	return names, nil
}

// LayoutChanged reports whether the set of top-level files and directories
// differs between sha and ref. Unknown (error) counts as changed.
func LayoutChanged(ctx context.Context, repoPath, sha, ref string) bool {
	if !gitcmd.IsValidRef(sha) || !gitcmd.IsValidRef(ref) {
		return true
	}
	a, err1 := topLevel(ctx, repoPath, sha)
	b, err2 := topLevel(ctx, repoPath, ref)
	if err1 != nil || err2 != nil {
		return true
	}
	return strings.Join(a, "\x00") != strings.Join(b, "\x00")
}

// Checkout is a scratch detached worktree of one commit, made read-only on
// disk so even a run as the same OS user cannot modify it.
type Checkout struct {
	Dir      string
	repoPath string
}

// NewCheckout adds a detached worktree of sha under a fresh temp dir. The
// repo's own worktree dir and branches are untouched; nothing is pushed.
func NewCheckout(ctx context.Context, repoPath, sha string) (*Checkout, error) {
	if !gitcmd.IsValidRef(sha) {
		return nil, fmt.Errorf("invalid sha %q", sha)
	}
	root, err := os.MkdirTemp("", "ate-repomap-")
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(root, "repo")
	lock := agent.RepoGitLock(repoPath)
	lock.Lock()
	out, err := gitcmd.Combined(ctx, repoPath, "worktree", "add", "--detach", dir, sha)
	lock.Unlock()
	if err != nil {
		_ = os.RemoveAll(root)
		return nil, fmt.Errorf("git worktree add: %w: %s", err, strings.TrimSpace(string(out)))
	}
	if err := os.Chmod(root, 0o755); err != nil {
		_ = os.RemoveAll(root)
		return nil, err
	}
	c := &Checkout{Dir: dir, repoPath: repoPath}
	makeReadOnly(dir)
	return c, nil
}

// Close removes the checkout and its git admin entry.
func (c *Checkout) Close() {
	if c == nil || c.Dir == "" {
		return
	}
	makeWritable(c.Dir)
	ctx := context.Background()
	lock := agent.RepoGitLock(c.repoPath)
	lock.Lock()
	_ = gitcmd.Run(ctx, c.repoPath, "worktree", "remove", "--force", c.Dir)
	_ = gitcmd.Run(ctx, c.repoPath, "worktree", "prune")
	lock.Unlock()
	_ = os.RemoveAll(filepath.Dir(c.Dir))
	c.Dir = ""
}

func makeReadOnly(dir string) {
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		if info, ierr := d.Info(); ierr == nil {
			_ = os.Chmod(p, info.Mode().Perm()&^0o222)
		}
		return nil
	})
}

func makeWritable(dir string) {
	// Directories first (top-down) so their children become reachable.
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if d != nil && d.Type()&fs.ModeSymlink == 0 {
			if info, ierr := d.Info(); ierr == nil {
				_ = os.Chmod(p, info.Mode().Perm()|0o200)
			}
		}
		return nil
	})
}

// IsReadOnly reports whether the checkout root rejects new files (used by
// tests and as a post-condition before a run).
func (c *Checkout) IsReadOnly() bool {
	f, err := os.CreateTemp(c.Dir, ".probe-")
	if err == nil {
		name := f.Name()
		_ = f.Close()
		_ = os.Remove(name)
		return false
	}
	return true
}

// ExistingDocs returns the top-level doc files (AGENTS.md, CLAUDE.md, README*)
// present in the checkout; the map points to them instead of restating them.
func ExistingDocs(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var docs []string
	for _, e := range entries {
		n := e.Name()
		up := strings.ToUpper(n)
		if e.IsDir() {
			continue
		}
		if up == "AGENTS.MD" || up == "CLAUDE.MD" || up == "README" || strings.HasPrefix(up, "README.") {
			docs = append(docs, n)
		}
	}
	sort.Strings(docs)
	return docs
}

// AddDocPointers appends a pointer line to the Overview page naming the
// repo's existing docs (deterministic, independent of what the model wrote).
func AddDocPointers(pages []Page, docs []string) []Page {
	if len(docs) == 0 {
		return pages
	}
	quoted := make([]string, len(docs))
	for i, d := range docs {
		quoted[i] = "`" + d + "`"
	}
	line := "\n\n**Existing docs (read these; this map does not restate them):** " + strings.Join(quoted, ", ")
	for i := range pages {
		if pages[i].Slug == "overview" && !strings.Contains(pages[i].Body, "Existing docs (read these") {
			body := truncate(pages[i].Body, MaxPageChars-len([]rune(line)))
			pages[i].Body = body + line
		}
	}
	return pages
}

func dirExists(p string) bool { return gitcmd.DirExists(p) }

func runsAsRoot() bool { return os.Geteuid() == 0 }

// fetch refreshes remote refs, best effort (no remote or no network is fine).
func fetch(ctx context.Context, repoPath string) {
	lock := agent.RepoGitLock(repoPath)
	lock.Lock()
	defer lock.Unlock()
	_, _ = gitcmd.CombinedNoPrompt(ctx, repoPath, "fetch", "--prune")
}
