package agent

import (
	"sort"

	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

// ChangeSetMergeOrder derives the intended merge order of a change set's
// children from the dependency edges between them (ADR 0041). A task that
// depends on another merges after it. Edges whose endpoints are not both in
// children (e.g. parent->child edges) are ignored. It returns the child ids in
// order and ok=true only when at least one child-to-child edge exists and the
// graph has no cycle. Ties are broken by created_at then id so output is stable.
func ChangeSetMergeOrder(children []gen.Task, edges []gen.ListWorkflowDependencyEdgesRow) ([]string, bool) {
	byID := make(map[string]gen.Task, len(children))
	for _, c := range children {
		byID[c.ID] = c
	}
	indeg := make(map[string]int, len(children))
	next := map[string][]string{}
	seen := map[[2]string]bool{}
	n := 0
	for _, e := range edges {
		_, a := byID[e.DependsOnTaskID]
		_, b := byID[e.TaskID]
		if !a || !b || e.DependsOnTaskID == e.TaskID || seen[[2]string{e.DependsOnTaskID, e.TaskID}] {
			continue
		}
		seen[[2]string{e.DependsOnTaskID, e.TaskID}] = true
		next[e.DependsOnTaskID] = append(next[e.DependsOnTaskID], e.TaskID)
		indeg[e.TaskID]++
		n++
	}
	if n == 0 {
		return nil, false
	}
	less := func(x, y string) bool {
		tx, ty := byID[x], byID[y]
		if !tx.CreatedAt.Equal(ty.CreatedAt) {
			return tx.CreatedAt.Before(ty.CreatedAt)
		}
		return x < y
	}
	var ready []string
	for _, c := range children {
		if indeg[c.ID] == 0 {
			ready = append(ready, c.ID)
		}
	}
	order := make([]string, 0, len(children))
	for len(ready) > 0 {
		sort.Slice(ready, func(i, j int) bool { return less(ready[i], ready[j]) })
		id := ready[0]
		ready = ready[1:]
		order = append(order, id)
		for _, m := range next[id] {
			if indeg[m]--; indeg[m] == 0 {
				ready = append(ready, m)
			}
		}
	}
	if len(order) != len(children) {
		return nil, false // cycle
	}
	return order, true
}
