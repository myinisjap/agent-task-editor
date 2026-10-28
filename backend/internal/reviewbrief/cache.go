package reviewbrief

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/myinisjap/agent-task-editor/backend/internal/gitcmd"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

const (
	freshWindow   = 15 * time.Second // reuse without any git call
	idleEviction  = 24 * time.Hour
	maxEntries    = 500
	gitTimeout    = 15 * time.Second
	maxDiffBytes  = 5 << 20
	topFilesLimit = 10
)

// FileStat is one row of the changed-files summary.
type FileStat struct {
	Path    string `json:"path"`
	Status  string `json:"status"`
	Added   int    `json:"additions"`
	Removed int    `json:"deletions"`
}

// ChangeSummary summarises the branch diff.
type ChangeSummary struct {
	Files     int        `json:"files"`
	Additions int        `json:"additions"`
	Deletions int        `json:"deletions"`
	TopFiles  []FileStat `json:"top_files"` // up to 10, by churn
}

// DiffResult is the cached, diff-derived half of a brief.
type DiffResult struct {
	Available bool // false: no branch, invalid ref, or refs not resolvable
	Changed   ChangeSummary
	Flags     []Flag
	Truncated bool // diff exceeded the size limit; summary is best effort
}

type entry struct {
	refs      string // branch|baseRef|notes hash: cheap freshness check
	key       string // head shas + notes hash
	checkedAt time.Time
	usedAt    time.Time
	res       DiffResult
}

// Cache memoises DiffResults per task, invalidated when the branch head, the
// base ref head or the task's agent_notes change.
type Cache struct {
	mu  sync.Mutex
	m   map[string]*entry
	now func() time.Time
}

// NewCache returns an empty Cache.
func NewCache() *Cache { return &Cache{m: map[string]*entry{}, now: time.Now} }

func notesHash(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// Diff returns the diff-derived summary and flags for the task. Failures to
// resolve the branch yield Available=false rather than an error; only
// unexpected errors (repo lookup) are returned.
func (c *Cache) Diff(ctx context.Context, q *gen.Queries, task gen.Task) (DiffResult, error) {
	empty := DiffResult{Changed: ChangeSummary{TopFiles: []FileStat{}}, Flags: []Flag{}}
	if task.Branch == "" || !gitcmd.IsValidRef(task.Branch) || !gitcmd.IsValidRef(task.BaseRef) {
		return empty, nil
	}
	nh := notesHash(task.AgentNotes)
	refs := task.Branch + "|" + task.BaseRef + "|" + nh
	now := c.now()

	c.mu.Lock()
	if e, ok := c.m[task.ID]; ok && e.refs == refs && now.Sub(e.checkedAt) < freshWindow {
		e.usedAt = now
		res := e.res
		c.mu.Unlock()
		return res, nil
	}
	c.mu.Unlock()

	gctx, cancel := context.WithTimeout(ctx, gitTimeout)
	defer cancel()
	gitDir, err := GitDir(gctx, q, task)
	if err != nil {
		return empty, err
	}
	out, err := gitcmd.Output(gctx, gitDir, "rev-parse", task.Branch, task.BaseRef)
	if err != nil {
		return empty, nil
	}
	key := strings.Join(strings.Fields(string(out)), ":") + ":" + nh

	c.mu.Lock()
	if e, ok := c.m[task.ID]; ok && e.refs == refs && e.key == key {
		e.checkedAt, e.usedAt = now, now
		res := e.res
		c.mu.Unlock()
		return res, nil
	}
	c.mu.Unlock()

	diff, err := TaskDiff(gctx, gitDir, task.BaseRef, task.Branch)
	if err != nil {
		return empty, nil
	}
	res := compute(diff, task.AgentNotes)

	c.mu.Lock()
	c.m[task.ID] = &entry{refs: refs, key: key, checkedAt: now, usedAt: now, res: res}
	c.evictLocked(now)
	c.mu.Unlock()
	return res, nil
}

func (c *Cache) evictLocked(now time.Time) {
	for id, e := range c.m {
		if now.Sub(e.usedAt) > idleEviction {
			delete(c.m, id)
		}
	}
	for len(c.m) > maxEntries {
		oldest, oldestAt := "", time.Time{}
		for id, e := range c.m {
			if oldest == "" || e.usedAt.Before(oldestAt) {
				oldest, oldestAt = id, e.usedAt
			}
		}
		delete(c.m, oldest)
	}
}

func compute(diff, notes string) DiffResult {
	files := ParseUnifiedDiff(diff)
	cs := ChangeSummary{Files: len(files), TopFiles: []FileStat{}}
	stats := make([]FileStat, 0, len(files))
	for _, f := range files {
		cs.Additions += f.Added
		cs.Deletions += f.Removed
		stats = append(stats, FileStat{Path: f.Path, Status: f.Status, Added: f.Added, Removed: f.Removed})
	}
	sort.SliceStable(stats, func(i, j int) bool {
		return stats[i].Added+stats[i].Removed > stats[j].Added+stats[j].Removed
	})
	if len(stats) > topFilesLimit {
		stats = stats[:topFilesLimit]
	}
	cs.TopFiles = append(cs.TopFiles, stats...)
	return DiffResult{
		Available: true,
		Changed:   cs,
		Flags:     ComputeFlags(files, notes),
		Truncated: len(diff) > maxDiffBytes,
	}
}
