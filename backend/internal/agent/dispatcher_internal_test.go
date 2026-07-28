package agent

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/myinisjap/agent-task-editor/backend/internal/storage"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

func cfg(name, labelsJSON string) gen.AgentConfig {
	return gen.AgentConfig{ID: name, Name: name, Labels: labelsJSON, Enabled: 1}
}

func disabledCfg(name, labelsJSON string) gen.AgentConfig {
	return gen.AgentConfig{ID: name, Name: name, Labels: labelsJSON, Enabled: 0}
}

func TestMatchConfigs(t *testing.T) {
	tests := []struct {
		name    string
		configs []gen.AgentConfig
		label   string
		want    []string // matched config names, in expected order; nil for no matches
	}{
		{"no match", []gen.AgentConfig{cfg("a", `["plan"]`)}, "review", nil},
		{"single match", []gen.AgentConfig{cfg("a", `["plan","review"]`)}, "review", []string{"a"}},
		// configs are newest-first; both matches returned in slice order.
		{"multiple matches returned in order", []gen.AgentConfig{cfg("new", `["review"]`), cfg("old", `["review"]`)}, "review", []string{"new", "old"}},
		// unparseable labels are skipped, not fatal — the valid config still matches.
		{"skips bad json", []gen.AgentConfig{cfg("broken", `not json`), cfg("good", `["review"]`)}, "review", []string{"good"}},
		{"all bad json", []gen.AgentConfig{cfg("broken", `{`)}, "review", nil},
		// disabled configs are skipped even if their label matches.
		{"skips disabled", []gen.AgentConfig{disabledCfg("off", `["review"]`)}, "review", nil},
		{"disabled then enabled", []gen.AgentConfig{disabledCfg("off", `["review"]`), cfg("on", `["review"]`)}, "review", []string{"on"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := matchConfigs(tt.configs, tt.label)
			if len(got) != len(tt.want) {
				t.Fatalf("want %v, got %v", tt.want, namesOf(got))
			}
			for i, w := range tt.want {
				if got[i].Name != w {
					t.Fatalf("want %v, got %v", tt.want, namesOf(got))
				}
			}
		})
	}
}

func namesOf(configs []*gen.AgentConfig) []string {
	names := make([]string, len(configs))
	for i, c := range configs {
		names[i] = c.Name
	}
	return names
}

// TestMatchConfigs_PriorityOrdering verifies matchConfigs preserves whatever
// order the input slice is given in — the priority-asc/created_at-desc sort
// happens in SQL (ListAgentConfigs), not in matchConfigs itself, so this
// confirms matchConfigs doesn't re-sort or otherwise disturb that order.
func TestMatchConfigs_PriorityOrdering(t *testing.T) {
	// Simulates the SQL order for three configs sharing a label with
	// priorities 0, 0, 1 (tie broken by created_at DESC, i.e. newest first).
	configs := []gen.AgentConfig{
		cfg("newest-prio0", `["review"]`),
		cfg("oldest-prio0", `["review"]`),
		cfg("prio1-backup", `["review"]`),
	}
	got := matchConfigs(configs, "review")
	want := []string{"newest-prio0", "oldest-prio0", "prio1-backup"}
	if len(got) != len(want) {
		t.Fatalf("want %v, got %v", want, namesOf(got))
	}
	for i, w := range want {
		if got[i].Name != w {
			t.Fatalf("want %v, got %v", want, namesOf(got))
		}
	}
}

// TestEffectiveBudget covers the min-of-(task, config)-nonzero-values
// semantics used by the dispatcher's cost-budget guard: a zero value from
// either source means "no cap from that source", and when both are set the
// stricter (lower) one wins.
func TestEffectiveBudget(t *testing.T) {
	tests := []struct {
		name       string
		taskBudget float64
		cfgBudget  float64
		wantBudget float64
	}{
		{"both zero: unlimited", 0, 0, 0},
		{"only task set", 5, 0, 5},
		{"only config set", 0, 10, 10},
		{"both set, task lower wins", 5, 10, 5},
		{"both set, config lower wins", 10, 5, 5},
		{"both set, equal", 7.5, 7.5, 7.5},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := effectiveBudget(tt.taskBudget, tt.cfgBudget)
			if got != tt.wantBudget {
				t.Fatalf("effectiveBudget(%v, %v) = %v, want %v", tt.taskBudget, tt.cfgBudget, got, tt.wantBudget)
			}
		})
	}
}

// TestToAgentConfig_CommandFilters verifies that CommandAllowlist/CommandDenylist
// JSON columns are unmarshalled into the corresponding AgentConfig slice fields,
// and that malformed/empty JSON falls back to nil (no restriction) rather than
// erroring, mirroring the existing EnabledPlugins/EnabledMCPServers behavior.
func TestToAgentConfig_CommandFilters(t *testing.T) {
	t.Run("populated lists round-trip", func(t *testing.T) {
		g := gen.AgentConfig{
			ID:               "a",
			CommandAllowlist: `["git *", "npm test"]`,
			CommandDenylist:  `["rm -rf *"]`,
		}
		pc := gen.ProviderConfig{ID: "pc-a", Env: "{}"}
		got := toAgentConfig(g, pc)
		wantAllow := []string{"git *", "npm test"}
		if len(got.CommandAllowlist) != len(wantAllow) {
			t.Fatalf("CommandAllowlist = %+v, want %+v", got.CommandAllowlist, wantAllow)
		}
		for i, w := range wantAllow {
			if got.CommandAllowlist[i] != w {
				t.Fatalf("CommandAllowlist[%d] = %q, want %q", i, got.CommandAllowlist[i], w)
			}
		}
		wantDeny := []string{"rm -rf *"}
		if len(got.CommandDenylist) != len(wantDeny) || got.CommandDenylist[0] != wantDeny[0] {
			t.Fatalf("CommandDenylist = %+v, want %+v", got.CommandDenylist, wantDeny)
		}
	})

	t.Run("default empty-array JSON yields nil slices", func(t *testing.T) {
		g := gen.AgentConfig{
			ID:               "a",
			CommandAllowlist: "[]",
			CommandDenylist:  "[]",
		}
		pc := gen.ProviderConfig{ID: "pc-a", Env: "{}"}
		got := toAgentConfig(g, pc)
		if len(got.CommandAllowlist) != 0 {
			t.Fatalf("expected empty CommandAllowlist, got %+v", got.CommandAllowlist)
		}
		if len(got.CommandDenylist) != 0 {
			t.Fatalf("expected empty CommandDenylist, got %+v", got.CommandDenylist)
		}
	})

	t.Run("malformed JSON falls back to nil, not an error", func(t *testing.T) {
		g := gen.AgentConfig{
			ID:               "a",
			CommandAllowlist: "not json",
			CommandDenylist:  "",
		}
		pc := gen.ProviderConfig{ID: "pc-a", Env: "{}"}
		got := toAgentConfig(g, pc)
		if got.CommandAllowlist != nil {
			t.Fatalf("expected nil CommandAllowlist on malformed JSON, got %+v", got.CommandAllowlist)
		}
		if got.CommandDenylist != nil {
			t.Fatalf("expected nil CommandDenylist on empty string, got %+v", got.CommandDenylist)
		}
	})
}

// TestDispatcher_repoAtLimit covers the effective-limit resolution used by
// the sweep dispatch path (see issue #255): repos.max_concurrent_runs wins
// when set to a positive value; otherwise the pool's global MAX_WORKERS is
// the fallback, so an unset limit preserves pre-existing behavior exactly.
func TestDispatcher_repoAtLimit(t *testing.T) {
	i64 := func(n int64) *int64 { return &n }

	pool := &Pool{maxWorkers: 5}
	d := &Dispatcher{pool: pool}

	tests := []struct {
		name    string
		repo    gen.Repo
		inUse   map[string]int64
		atLimit bool
	}{
		{"unset limit, under global cap", gen.Repo{ID: "r1"}, map[string]int64{"r1": 4}, false},
		{"unset limit, at global cap", gen.Repo{ID: "r1"}, map[string]int64{"r1": 5}, true},
		{"unset limit, no in-flight runs", gen.Repo{ID: "r1"}, map[string]int64{}, false},
		{"repo limit under repo cap", gen.Repo{ID: "r1", MaxConcurrentRuns: i64(2)}, map[string]int64{"r1": 1}, false},
		{"repo limit at repo cap, below global cap", gen.Repo{ID: "r1", MaxConcurrentRuns: i64(2)}, map[string]int64{"r1": 2}, true},
		{"zero repo limit treated as unset (falls back to global)", gen.Repo{ID: "r1", MaxConcurrentRuns: i64(0)}, map[string]int64{"r1": 4}, false},
		{"different repo's in-use count doesn't affect this repo", gen.Repo{ID: "r1", MaxConcurrentRuns: i64(1)}, map[string]int64{"r2": 10}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := d.repoAtLimit(tt.repo, tt.inUse)
			if got != tt.atLimit {
				t.Errorf("repoAtLimit() = %v, want %v", got, tt.atLimit)
			}
		})
	}
}

// TestDispatcher_LastSweep_SetBeforeLoopStarts verifies Run records an
// initial heartbeat before entering its ticker loop, so /readyz doesn't see
// a zero LastSweep during the window before the first tick.
func TestDispatcher_LastSweep_SetBeforeLoopStarts(t *testing.T) {
	f, err := os.CreateTemp("", "dispatcher-heartbeat-*.db")
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	t.Cleanup(func() { _ = os.Remove(f.Name()) })

	db, err := storage.Open(f.Name())
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	d := NewDispatcher(db.SQL(), &Pool{maxWorkers: 1}, nil, nil)
	// Use a long interval so this test only exercises the pre-loop
	// heartbeat, not a real tick.
	d.interval = time.Hour

	if !d.LastSweep().IsZero() {
		t.Fatal("expected LastSweep to be zero before Run starts")
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		d.Run(ctx)
		close(done)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if !d.LastSweep().IsZero() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("expected LastSweep to be set shortly after Run starts, still zero")
}

// TestProviderSupportsResume documents which providers' resume paths are
// wired up in the dispatcher (see issue #281). qwen_code, codex_cli, and
// opencode join claude here because their session-id recording and resume
// invocation are both verified correct. gemini_cli is deliberately excluded:
// its resume invocation is correct too, but GeminiRunner's per-run
// GEMINI_CLI_HOME temp dir is deleted on cleanup, destroying session storage
// before it could ever be resumed (tracked in #284) — enabling it here would
// silently no-op instead of resuming.
func TestProviderSupportsResume(t *testing.T) {
	tests := []struct {
		provider string
		want     bool
	}{
		{"claude", true},
		{"qwen_code", true},
		{"codex_cli", true},
		{"opencode", true},
		{"gemini_cli", false},
		{"unknown_provider", false},
		{"", false},
	}
	for _, tt := range tests {
		t.Run(tt.provider, func(t *testing.T) {
			got := providerSupportsResume(tt.provider)
			if got != tt.want {
				t.Errorf("providerSupportsResume(%q) = %v, want %v", tt.provider, got, tt.want)
			}
		})
	}
}

// TestDispatcher_ResolveAgentConfig_ResumeByProvider is an integration-style
// test (real sqlite, real queries) of resolveAgentConfig: it seeds a task
// with a completed run that recorded a session id, then verifies that
// providers wired into providerSupportsResume get that session id back while
// gemini_cli — deliberately excluded pending #284 — does not, even though a
// session was recorded for it too. This is the regression test for issue
// #281 ("session resume is silently claude-only").
func TestDispatcher_ResolveAgentConfig_ResumeByProvider(t *testing.T) {
	f, err := os.CreateTemp("", "dispatcher-resume-*.db")
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	t.Cleanup(func() { _ = os.Remove(f.Name()) })

	db, err := storage.Open(f.Name())
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	ctx := context.Background()
	q := gen.New(db.SQL())
	d := NewDispatcher(db.SQL(), &Pool{maxWorkers: 1}, nil, nil)

	wfID := "wf-1"
	if _, err := q.CreateWorkflow(ctx, gen.CreateWorkflowParams{ID: wfID, Name: "wf", Description: "test"}); err != nil {
		t.Fatalf("create workflow: %v", err)
	}
	repoID := "repo-1"
	if _, err := q.CreateRepo(ctx, gen.CreateRepoParams{ID: repoID, Name: "repo", Path: "/tmp/repo", WorkflowID: &wfID}); err != nil {
		t.Fatalf("create repo: %v", err)
	}

	for _, provider := range []string{"qwen_code", "codex_cli", "opencode", "gemini_cli"} {
		provider := provider
		t.Run(provider, func(t *testing.T) {
			pcID := provider + "-pc"
			if _, err := q.CreateProviderConfig(ctx, gen.CreateProviderConfigParams{
				ID: pcID, Name: provider, Provider: provider, Model: "none", Env: `{}`,
			}); err != nil {
				t.Fatalf("create provider config: %v", err)
			}
			acID := provider + "-ac"
			if _, err := q.CreateAgentConfig(ctx, gen.CreateAgentConfigParams{
				ID: acID, Name: provider + "-agent", ProviderConfigID: pcID,
				Labels: `["ready"]`, MaxRetries: 1, RetryBackoffSecs: 1, ResumeSessions: 1,
			}); err != nil {
				t.Fatalf("create agent config: %v", err)
			}
			taskID := provider + "-task"
			if _, err := q.CreateTask(ctx, gen.CreateTaskParams{
				ID: taskID, Title: "t", WorkflowID: wfID, RepoID: repoID, Label: "ready",
			}); err != nil {
				t.Fatalf("create task: %v", err)
			}
			run, err := q.CreateAgentRun(ctx, gen.CreateAgentRunParams{ID: provider + "-run", TaskID: taskID, AgentConfigID: &acID})
			if err != nil {
				t.Fatalf("create agent run: %v", err)
			}
			if err := q.SetAgentRunSession(ctx, gen.SetAgentRunSessionParams{ID: run.ID, SessionID: "sess-" + provider}); err != nil {
				t.Fatalf("set agent run session: %v", err)
			}

			matched, err := q.GetAgentConfig(ctx, acID)
			if err != nil {
				t.Fatalf("get agent config: %v", err)
			}
			task, err := q.GetTask(ctx, taskID)
			if err != nil {
				t.Fatalf("get task: %v", err)
			}

			_, resumeSessionID, err := d.resolveAgentConfig(ctx, task, matched)
			if err != nil {
				t.Fatalf("resolveAgentConfig: %v", err)
			}

			if provider == "gemini_cli" {
				if resumeSessionID != "" {
					t.Errorf("gemini_cli: expected no resume session (pending #284), got %q", resumeSessionID)
				}
				return
			}
			if resumeSessionID != "sess-"+provider {
				t.Errorf("%s: expected resume session %q, got %q", provider, "sess-"+provider, resumeSessionID)
			}
		})
	}
}

// TestDispatcher_LastSweep_AdvancesOnTick verifies LastSweep advances as the
// dispatch loop ticks.
func TestDispatcher_LastSweep_AdvancesOnTick(t *testing.T) {
	f, err := os.CreateTemp("", "dispatcher-heartbeat-tick-*.db")
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	t.Cleanup(func() { _ = os.Remove(f.Name()) })

	db, err := storage.Open(f.Name())
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	d := NewDispatcher(db.SQL(), &Pool{maxWorkers: 1}, nil, nil)
	d.interval = 10 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		d.Run(ctx)
		close(done)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})

	first := d.LastSweep()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cur := d.LastSweep(); cur.After(first) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("expected LastSweep to advance after ticks, it did not")
}
