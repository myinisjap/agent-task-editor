package ghsync

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

const (
	// webhookDebounce is how long a webhook-triggered refresh waits before
	// running, coalescing the burst of events GitHub emits for one push
	// (synchronize + check_suite + status ...) into one forge refresh per task.
	// It also gives GitHub's read side a moment to catch up with the event.
	webhookDebounce = 2 * time.Second
	// webhookQuietWindow is how recently a webhook must have been received for
	// the polling sweep to back off to webhookFallbackFactor x its interval.
	webhookQuietWindow = 10 * time.Minute
	// webhookFallbackFactor multiplies the poll interval while webhooks are
	// flowing. Polling never stops: it is the safety net for missed deliveries.
	webhookFallbackFactor = 4
	// webhookRefreshTimeout bounds one webhook-triggered refresh.
	webhookRefreshTimeout = 2 * time.Minute
)

// PRRef identifies the PR(s) a webhook event concerns. Fields come from the
// (signature-verified) payload but are used ONLY to pick which tasks to
// refresh; the refreshed state itself is always re-fetched from the forge.
type PRRef struct {
	// Repo is the "owner/name" the event came from.
	Repo string
	// Numbers are PR numbers the event refers to.
	Numbers []int
	// Branches are head branch names the event refers to.
	Branches []string
	// HeadSHA is the commit a check/status event refers to.
	HeadSHA string
}

// NoteWebhook records that a webhook delivery was received now, which lets the
// polling sweep run at a longer interval (see Run).
func (s *Syncer) NoteWebhook() {
	s.webhookMu.Lock()
	s.lastWebhook = time.Now()
	s.webhookMu.Unlock()
}

func (s *Syncer) webhookRecent(now time.Time) bool {
	s.webhookMu.Lock()
	defer s.webhookMu.Unlock()
	return !s.lastWebhook.IsZero() && now.Sub(s.lastWebhook) < webhookQuietWindow
}

// prNumberFromURL extracts N from ".../pull/N" ("" or other shapes yield 0).
func prNumberFromURL(u string) int {
	i := strings.LastIndex(u, "/pull/")
	if i < 0 {
		return 0
	}
	rest := u[i+len("/pull/"):]
	if j := strings.IndexAny(rest, "/?#"); j >= 0 {
		rest = rest[:j]
	}
	n, err := strconv.Atoi(rest)
	if err != nil || n < 0 {
		return 0
	}
	return n
}

func (r PRRef) matches(task gen.Task, storedSHA string) bool {
	if n := prNumberFromURL(task.PrUrl); n != 0 && slices.Contains(r.Numbers, n) {
		return true
	}
	if task.Branch != "" && slices.Contains(r.Branches, task.Branch) {
		return true
	}
	return r.HeadSHA != "" && storedSHA != "" && strings.EqualFold(r.HeadSHA, storedSHA)
}

// RefreshMatching finds the poll-eligible tasks whose PR matches ref (same
// repo, and PR number, head branch, or head SHA) and schedules a debounced
// refresh of each via the same syncTask path the polling sweep uses. It
// returns the number of tasks matched. The refresh itself runs asynchronously.
func (s *Syncer) RefreshMatching(ctx context.Context, ref PRRef) int {
	tasks, err := s.q.ListGhSyncEligibleTasks(ctx)
	if err != nil {
		slog.Warn("ghsync: webhook list tasks failed", "err", err)
		return 0
	}
	repos := map[string]repoInfo{}
	matched := 0
	for _, task := range tasks {
		info, ok := repos[task.RepoID]
		if !ok {
			info = s.resolveRepoInfo(ctx, task.RepoID)
			repos[task.RepoID] = info
		}
		if info.ghName == "" || !strings.EqualFold(info.ghName, ref.Repo) {
			continue
		}
		var sha string
		if st, err := s.q.GetTaskPRReviewState(ctx, task.ID); err == nil {
			sha = st.HeadSha
		} else if !errors.Is(err, sql.ErrNoRows) {
			slog.Warn("ghsync: webhook get review state", "task_id", task.ID, "err", err)
		}
		if ref.matches(task, sha) {
			matched++
			s.scheduleRefresh(task.ID)
		}
	}
	return matched
}

// scheduleRefresh queues one refresh of the task after the debounce delay;
// events arriving while one is already queued coalesce into it.
func (s *Syncer) scheduleRefresh(taskID string) {
	s.webhookMu.Lock()
	if s.pending == nil {
		s.pending = map[string]bool{}
	}
	if s.pending[taskID] {
		s.webhookMu.Unlock()
		return
	}
	s.pending[taskID] = true
	s.webhookMu.Unlock()

	time.AfterFunc(s.debounce, func() {
		// Clear before running so an event that lands mid-refresh schedules
		// a fresh one rather than being lost.
		s.webhookMu.Lock()
		delete(s.pending, taskID)
		s.webhookMu.Unlock()
		s.refreshTask(taskID)
	})
}

// refreshTask re-reads the task and runs the standard syncTask against the
// forge, exactly as a sweep would for an eligible task.
func (s *Syncer) refreshTask(taskID string) {
	ctx, cancel := context.WithTimeout(context.Background(), webhookRefreshTimeout)
	defer cancel()
	log := slog.With("component", "ghsync", "task_id", taskID)
	task, err := s.q.GetTask(ctx, taskID)
	if err != nil {
		log.Warn("ghsync: webhook refresh get task", "err", err)
		return
	}
	if task.Branch == "" || task.Archived != 0 || task.GitState == "pr_merged" || task.GitState == "pr_closed" {
		return
	}
	info := s.resolveRepoInfo(ctx, task.RepoID)
	if info.ghName == "" {
		return
	}
	s.syncMu.Lock()
	ok := s.syncTask(ctx, task, info)
	s.syncMu.Unlock()
	if ok {
		s.recordForgeSuccess(task.RepoID)
	} else {
		s.recordForgeFailure(task.RepoID, time.Now())
	}
}
