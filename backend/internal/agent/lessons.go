package agent

import (
	"context"
	"log/slog"
	"regexp"
	"sort"
	"strings"

	"github.com/myinisjap/agent-task-editor/backend/internal/gitcmd"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

const (
	maxInjectedLessons     = 15
	maxInjectedLessonChars = 2000
)

// SplitLessonList parses the storage format of a repo lesson's path_globs
// (newline separated) or stages (comma separated) column: it splits on both,
// trims, and drops empty entries.
func SplitLessonList(s string) []string {
	out := []string{}
	for _, p := range strings.FieldsFunc(s, func(r rune) bool { return r == '\n' || r == '\r' || r == ',' }) {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// lessonLineLen is the rendered size of a lesson in the prompt ("- [kind] body\n").
func lessonLineLen(l gen.RepoLesson) int {
	return len("- [") + len(l.Kind) + len("] ") + len(l.Body) + 1
}

// selectLessons filters active lesson rows by stage label and changed files,
// orders them (global or stage-specific first, then newest) and applies the
// count/character budget. Pure: no I/O.
func selectLessons(rows []gen.RepoLesson, label string, changedFiles []string) []gen.RepoLesson {
	cache := map[string]*regexp.Regexp{}
	var kept []gen.RepoLesson
	for _, l := range rows {
		if stages := SplitLessonList(l.Stages); len(stages) > 0 {
			found := false
			for _, s := range stages {
				if s == label {
					found = true
					break
				}
			}
			if !found {
				continue
			}
		}
		if globs := SplitLessonList(l.PathGlobs); len(globs) > 0 {
			match := false
			for _, f := range changedFiles {
				for _, g := range globs {
					if matchLessonGlobCached(cache, g, f) {
						match = true
						break
					}
				}
				if match {
					break
				}
			}
			if !match {
				continue
			}
		}
		kept = append(kept, l)
	}
	tier := func(l gen.RepoLesson) int {
		if l.RepoID == nil || len(SplitLessonList(l.Stages)) > 0 {
			return 0
		}
		return 1
	}
	sort.SliceStable(kept, func(i, j int) bool {
		ti, tj := tier(kept[i]), tier(kept[j])
		if ti != tj {
			return ti < tj
		}
		if !kept[i].CreatedAt.Equal(kept[j].CreatedAt) {
			return kept[i].CreatedAt.After(kept[j].CreatedAt)
		}
		return kept[i].ID > kept[j].ID
	})
	var out []gen.RepoLesson
	chars := 0
	for _, l := range kept {
		if len(out) >= maxInjectedLessons {
			break
		}
		n := lessonLineLen(l)
		if chars+n > maxInjectedLessonChars {
			continue
		}
		chars += n
		out = append(out, l)
	}
	return out
}

func matchLessonGlobCached(cache map[string]*regexp.Regexp, pattern, file string) bool {
	re, ok := cache[pattern]
	if !ok {
		re = compileLessonGlob(pattern)
		cache[pattern] = re
	}
	return re != nil && re.MatchString(file)
}

// matchLessonGlob matches a repo-relative file path against a glob. Supports
// ** (any number of segments, including zero), * (anything but /) and ?.
// A pattern without "/" also matches the basename (gitignore-style); a
// trailing "/" means a directory prefix.
func matchLessonGlob(pattern, file string) bool {
	return matchLessonGlobCached(map[string]*regexp.Regexp{}, pattern, file)
}

func compileLessonGlob(pattern string) *regexp.Regexp {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return nil
	}
	prefix := false
	if strings.HasSuffix(pattern, "/") {
		prefix = true
		pattern = strings.TrimLeft(pattern, "/")
		pattern += "**"
	}
	var b strings.Builder
	b.WriteString("^")
	if !strings.Contains(strings.TrimSuffix(pattern, "**"), "/") && !prefix {
		b.WriteString("(?:.*/)?") // basename match
	}
	for i := 0; i < len(pattern); i++ {
		c := pattern[i]
		switch c {
		case '*':
			if i+1 < len(pattern) && pattern[i+1] == '*' {
				i++
				if i+1 < len(pattern) && pattern[i+1] == '/' {
					i++
					b.WriteString("(?:.*/)?") // "**/" = zero or more dirs
				} else {
					b.WriteString(".*")
				}
			} else {
				b.WriteString("[^/]*")
			}
		case '?':
			b.WriteString("[^/]")
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	b.WriteString("$")
	re, err := regexp.Compile(b.String())
	if err != nil {
		return nil
	}
	return re
}

// lessonSelector returns the Job.SelectLessons closure for task t. It runs in
// the pool worker after the worktree exists, so git never runs in the sweep.
// Best effort: any failure just yields fewer (or no) lessons.
func (d *Dispatcher) lessonSelector(t gen.Task, log *slog.Logger) func(ctx context.Context, workDir, branch string, resuming bool) []RepoLesson {
	return func(ctx context.Context, workDir, branch string, resuming bool) []RepoLesson {
		repoID := t.RepoID
		rows, err := d.q.ListActiveLessonsForRepo(ctx, &repoID)
		if err != nil {
			log.Warn("dispatcher: list repo lessons", "err", err)
			return nil
		}
		if len(rows) == 0 {
			return nil
		}
		var files []string
		needFiles := false
		for _, r := range rows {
			if len(SplitLessonList(r.PathGlobs)) > 0 {
				needFiles = true
				break
			}
		}
		if needFiles && workDir != "" && branch != "" && t.BaseRef != "" &&
			gitcmd.IsValidRef(branch) && gitcmd.IsValidRef(t.BaseRef) {
			f, ferr := changedFilesAgainstBase(ctx, workDir, t.BaseRef, branch)
			if ferr != nil {
				log.Warn("dispatcher: list changed files for lessons", "err", ferr)
			} else {
				files = f
			}
		}
		sel := selectLessons(rows, t.Label, files)
		if len(sel) == 0 {
			return nil
		}
		out := make([]RepoLesson, 0, len(sel))
		ids := make([]string, 0, len(sel))
		for _, l := range sel {
			out = append(out, RepoLesson{ID: l.ID, Kind: l.Kind, Body: l.Body})
			ids = append(ids, l.ID)
		}
		// A resumed session uses buildResumePrompt, which omits lessons, so it
		// does not count as an injection. (If a resume falls back to a cold
		// start the lessons are rendered but not counted; acceptable.)
		if !resuming {
			if err := d.q.IncrementLessonInjectedCount(ctx, ids); err != nil {
				log.Warn("dispatcher: increment lesson injected_count", "err", err)
			}
		}
		return out
	}
}
