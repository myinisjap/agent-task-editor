package agent

import (
	"log/slog"

	"context"
	"fmt"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
	"github.com/myinisjap/agent-task-editor/backend/internal/wsevent"
	"strings"
)

// File scopes for planner fan-out: a subtask may declare the repo-relative globs
// it is allowed to change. Scopes drive the sibling overlap guard (overlapping
// siblings are serialized) and a post-run warning for out-of-scope changes.

const (
	maxFileScopeEntries = 20
	maxFileScopeLen     = 256
)

// MatchFileGlob reports whether file matches the glob (same semantics as lesson
// globs: **, *, ?, basename match without "/", trailing "/" = directory prefix).
func MatchFileGlob(pattern, file string) bool { return matchLessonGlob(pattern, file) }

// OutOfScope returns the files that match none of the globs.
func OutOfScope(files, globs []string) []string {
	var out []string
	for _, f := range files {
		in := false
		for _, g := range globs {
			if MatchFileGlob(g, f) {
				in = true
				break
			}
		}
		if !in {
			out = append(out, f)
		}
	}
	return out
}

func hasWildcard(s string) bool { return strings.ContainsAny(s, "*?[") }

// literalDirPrefix is the text before the first wildcard, cut back to the last
// "/". A basename-only pattern (no "/") yields "" and so overlaps everything.
func literalDirPrefix(g string) string {
	if i := strings.IndexAny(g, "*?["); i >= 0 {
		g = g[:i]
		if j := strings.LastIndex(g, "/"); j >= 0 {
			return g[:j+1]
		}
		return ""
	}
	// No wildcard: a directory prefix ("a/b/") is its own prefix; a literal file
	// has its parent directory as prefix.
	if strings.HasSuffix(g, "/") {
		return g
	}
	if j := strings.LastIndex(g, "/"); j >= 0 {
		return g[:j+1]
	}
	return ""
}

func globPairOverlaps(x, y string) bool {
	if x == y {
		return true
	}
	if !hasWildcard(x) && MatchFileGlob(y, x) {
		return true
	}
	if !hasWildcard(y) && MatchFileGlob(x, y) {
		return true
	}
	px, py := literalDirPrefix(x), literalDirPrefix(y)
	return strings.HasPrefix(px, py) || strings.HasPrefix(py, px)
}

// GlobsOverlap is a deliberately conservative overlap test: a false positive
// only serializes two subtasks.
func GlobsOverlap(a, b []string) bool {
	return len(OverlappingGlobs(a, b)) > 0
}

// OverlappingGlobs returns the globs of a that overlap some glob of b.
func OverlappingGlobs(a, b []string) []string {
	var out []string
	for _, x := range a {
		for _, y := range b {
			if globPairOverlaps(x, y) {
				out = append(out, x)
				break
			}
		}
	}
	return out
}

// NormalizeFileScope trims, dedupes and validates a file scope list.
func NormalizeFileScope(in []string) ([]string, error) {
	out := []string{}
	seen := map[string]bool{}
	for _, g := range in {
		g = strings.TrimSpace(g)
		if g == "" || seen[g] {
			continue
		}
		if len(g) > maxFileScopeLen {
			return nil, fmt.Errorf("file_scope entry too long (max %d chars)", maxFileScopeLen)
		}
		if strings.ContainsAny(g, "\n\r\x00") {
			return nil, fmt.Errorf("file_scope entries must not contain newlines or NUL")
		}
		if strings.HasPrefix(g, "/") {
			return nil, fmt.Errorf("file_scope entries must be repo-relative (no leading /)")
		}
		for _, seg := range strings.Split(g, "/") {
			if seg == ".." {
				return nil, fmt.Errorf("file_scope entries must not contain '..' segments")
			}
		}
		seen[g] = true
		out = append(out, g)
	}
	if len(out) > maxFileScopeEntries {
		return nil, fmt.Errorf("too many file_scope entries (max %d)", maxFileScopeEntries)
	}
	return out, nil
}

// diffStatAgainstBase returns `git diff --stat` of the branch against its
// merge-base with baseRef, capped at maxLines lines. Empty on error.
func diffStatAgainstBase(ctx context.Context, wtPath, baseRef, branch string, maxLines int) string {
	if wtPath == "" || baseRef == "" || branch == "" {
		return ""
	}
	from := baseRef
	if mb, err := git(ctx, wtPath, "merge-base", baseRef, branch); err == nil {
		from = strings.TrimSpace(string(mb))
	}
	out, err := git(ctx, wtPath, "diff", "--stat", from, branch, "--")
	if err != nil {
		return ""
	}
	lines := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
	if len(lines) > maxLines {
		lines = append(lines[:maxLines], "…(truncated)")
	}
	return strings.Join(lines, "\n")
}

// splitFileScope parses the newline-joined stored form.
func splitFileScope(s string) []string {
	var out []string
	for _, p := range strings.Split(s, "\n") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// sourceCommentsEndMarkerForNotes mirrors providers' sourceCommentsEndMarker so
// subtask notes embedded in a parent prompt can't close an untrusted block.
const sourceCommentsEndMarkerForNotes = ">>>END UNTRUSTED SOURCE COMMENTS"

// checkFileScope warns (never fails) when a scoped subtask's run changed files
// outside its declared scope: the list is appended to the task's agent_notes so
// the next stage and the parent's integration prompt see it.
func (p *Pool) checkFileScope(ctx context.Context, job Job, finalStatus string, log *slog.Logger) {
	t := job.Input.Task
	if t.ParentID == "" || len(t.FileScope) == 0 || finalStatus != "completed" || job.Input.RepoPath == "" {
		return
	}
	task, err := p.q.GetTask(ctx, t.ID)
	if err != nil {
		log.Warn("pool: file scope check: load task", "err", err)
		return
	}
	files, err := changedFilesAgainstBase(ctx, job.Input.RepoPath, task.BaseRef, task.Branch)
	if err != nil {
		log.Warn("pool: file scope check", "err", err)
		return
	}
	out := OutOfScope(files, t.FileScope)
	if len(out) == 0 {
		return
	}
	const maxListed = 50
	var b strings.Builder
	b.WriteString(task.AgentNotes)
	fmt.Fprintf(&b, "\n\n## FILE SCOPE WARNING (automated check)\nThis subtask is scoped to: %s. These changed files fall outside it:\n", strings.Join(t.FileScope, ", "))
	for i, f := range out {
		if i >= maxListed {
			fmt.Fprintf(&b, "- …and %d more\n", len(out)-maxListed)
			break
		}
		b.WriteString("- " + f + "\n")
	}
	b.WriteString("(Warning only — explain or revert them.)")
	if _, err := p.q.UpdateTaskNotes(ctx, gen.UpdateTaskNotesParams{AgentNotes: b.String(), ID: t.ID}); err != nil {
		log.Warn("pool: file scope check: save notes", "err", err)
		return
	}
	log.Warn("pool: subtask changed files outside its file scope", "files", len(out))
	if p.pub != nil {
		p.pub.Publish(wsevent.TypeTaskUpdated, wsevent.TaskUpdated{ID: t.ID})
	}
}
