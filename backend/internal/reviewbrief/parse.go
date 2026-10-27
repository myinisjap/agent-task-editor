package reviewbrief

import "strings"

// maxStoredLines caps the added/removed lines kept per file (counting continues).
const maxStoredLines = 2000

// FileChange is one file of a unified diff.
type FileChange struct {
	Path         string
	OldPath      string
	Status       string // added|modified|deleted|renamed
	Added        int
	Removed      int
	AddedLines   []string
	RemovedLines []string
}

// ParseUnifiedDiff parses `git diff` output into per-file changes.
func ParseUnifiedDiff(diff string) []FileChange {
	var files []FileChange
	var cur *FileChange
	inHunk := false
	for _, line := range strings.Split(diff, "\n") {
		if strings.HasPrefix(line, "diff --git ") {
			files = append(files, FileChange{Status: "modified"})
			cur = &files[len(files)-1]
			inHunk = false
			rest := strings.TrimPrefix(line, "diff --git ")
			if i := strings.Index(rest, " b/"); i >= 0 {
				cur.OldPath = strings.TrimPrefix(rest[:i], "a/")
				cur.Path = rest[i+3:]
			}
			continue
		}
		if cur == nil {
			continue
		}
		if !inHunk {
			switch {
			case strings.HasPrefix(line, "new file mode"):
				cur.Status = "added"
			case strings.HasPrefix(line, "deleted file mode"):
				cur.Status = "deleted"
			case strings.HasPrefix(line, "rename from "):
				cur.Status = "renamed"
				cur.OldPath = strings.TrimPrefix(line, "rename from ")
			case strings.HasPrefix(line, "rename to "):
				cur.Status = "renamed"
				cur.Path = strings.TrimPrefix(line, "rename to ")
			case strings.HasPrefix(line, "@@"):
				inHunk = true
			}
			continue
		}
		if strings.HasPrefix(line, "@@") {
			continue
		}
		switch {
		case strings.HasPrefix(line, "+"):
			cur.Added++
			if len(cur.AddedLines) < maxStoredLines {
				cur.AddedLines = append(cur.AddedLines, line[1:])
			}
		case strings.HasPrefix(line, "-"):
			cur.Removed++
			if len(cur.RemovedLines) < maxStoredLines {
				cur.RemovedLines = append(cur.RemovedLines, line[1:])
			}
		}
	}
	for i := range files {
		if files[i].Status == "deleted" && files[i].OldPath != "" {
			files[i].Path = files[i].OldPath
		}
	}
	return files
}
