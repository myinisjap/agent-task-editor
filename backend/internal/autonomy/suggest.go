package autonomy

import (
	"path"
	"sort"
	"strings"
)

// SpecificExts are file extensions worth suggesting as a rule on their own.
var SpecificExts = map[string]bool{
	".sql": true, ".proto": true, ".graphql": true, ".gql": true, ".tf": true, ".tfvars": true,
	".yml": true, ".yaml": true, ".sh": true, ".lock": true, ".env": true,
}

// GlobSuggestion is one candidate path glob.
type GlobSuggestion struct {
	Glob  string `json:"glob"`
	Kind  string `json:"kind"` // dir | ext
	Files int    `json:"files"`
}

// Suggestions are candidate rule conditions derived from a task.
type Suggestions struct {
	PathGlobs       []GlobSuggestion `json:"path_globs"`
	TaskType        *string          `json:"task_type"`
	MinChangedLines *int             `json:"min_changed_lines"`
}

// Suggest derives candidate rule conditions from a task's changed files.
func Suggest(files []string, changedLines int, taskType string) Suggestions {
	out := Suggestions{PathGlobs: []GlobSuggestion{}}
	count := map[string]int{}
	extCount := map[string]int{}
	for _, f := range files {
		f = strings.Trim(f, "/")
		if f == "" {
			continue
		}
		for d := path.Dir(f); d != "." && d != "/" && d != ""; d = path.Dir(d) {
			count[d]++
		}
		if e := strings.ToLower(path.Ext(f)); SpecificExts[e] {
			extCount[e]++
		}
	}
	// A directory is a candidate when no direct child directory covers the
	// same set of files, so it is the deepest directory for that set.
	type cand struct {
		dir   string
		n     int
		depth int
	}
	var cands []cand
	for d, n := range count {
		covered := false
		for c, cn := range count {
			if cn == n && path.Dir(c) == d {
				covered = true
				break
			}
		}
		if !covered {
			cands = append(cands, cand{d, n, strings.Count(d, "/")})
		}
	}
	sort.Slice(cands, func(i, j int) bool {
		a, b := cands[i], cands[j]
		if a.n != b.n {
			return a.n > b.n
		}
		if a.depth != b.depth {
			return a.depth > b.depth
		}
		return a.dir < b.dir
	})
	for i, c := range cands {
		if i == 3 {
			break
		}
		out.PathGlobs = append(out.PathGlobs, GlobSuggestion{Glob: c.dir + "/**", Kind: "dir", Files: c.n})
	}
	exts := make([]string, 0, len(extCount))
	for e := range extCount {
		exts = append(exts, e)
	}
	sort.Slice(exts, func(i, j int) bool {
		if extCount[exts[i]] != extCount[exts[j]] {
			return extCount[exts[i]] > extCount[exts[j]]
		}
		return exts[i] < exts[j]
	})
	for _, e := range exts {
		out.PathGlobs = append(out.PathGlobs, GlobSuggestion{Glob: "*" + e, Kind: "ext", Files: extCount[e]})
	}
	if t := strings.TrimSpace(taskType); t != "" {
		out.TaskType = &t
	}
	if changedLines >= 10 {
		p := 1
		for changedLines/p >= 10 {
			p *= 10
		}
		v := changedLines / p * p
		out.MinChangedLines = &v
	}
	return out
}
