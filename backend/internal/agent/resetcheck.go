package agent

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"time"

	"github.com/google/uuid"
)

// ResetChecker finds out when a provider account whose limit carried no
// reset time (opencode, qwen_code, API-key accounts...) is usable again. It
// probes the account's CLI with a tiny prompt on the registry's schedule
// (resetCheckDelays), outside any task, and clears the block as soon as a
// probe gets through. Until then every task skips that account, so no task
// run is spent discovering the limit is still in place.
type ResetChecker struct {
	Limits *RateLimitRegistry
	// NewProvider builds the provider a probe runs on. It should leave out
	// the MCP sidecar: a probe has no task to report on.
	NewProvider func(AgentConfig) Provider
	// Tick is how often due probes are looked for (default 30s).
	Tick time.Duration
	// Timeout bounds one probe run (default 2 minutes).
	Timeout time.Duration
}

const resetCheckPrompt = "Rate-limit check from the task board. Reply with the single word OK and stop. Do not use any tools."

// Run enables reset checks on the registry and probes due pools until ctx is
// cancelled.
func (c *ResetChecker) Run(ctx context.Context) {
	if c == nil || c.Limits == nil || c.NewProvider == nil {
		return
	}
	c.Limits.EnableResetChecks()
	tick := c.Tick
	if tick <= 0 {
		tick = 30 * time.Second
	}
	t := time.NewTicker(tick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			c.checkDue(ctx)
		}
	}
}

// checkDue probes every pool whose check is due, one at a time.
func (c *ResetChecker) checkDue(ctx context.Context) {
	for _, rc := range c.Limits.dueResetChecks(time.Now()) {
		if ctx.Err() != nil {
			return
		}
		c.check(ctx, rc)
	}
}

func (c *ResetChecker) check(ctx context.Context, rc ResetCheck) {
	log := slog.With("component", "reset_check", "usage_pool", rc.Pool, "provider", rc.Config.Provider)
	err := c.probe(ctx, rc.Config)
	var rl *ErrRateLimit
	var te transientErr
	switch {
	case errors.As(err, &rl) && !rl.ResetAt.IsZero() && rl.ResetAt.After(time.Now()):
		// The limit now says when it resets; wait for that instead of probing.
		c.Limits.Block(rc.Pool, rl.ResetAt)
		log.Info("reset check: still limited, reset time now known", "reset_at", rl.ResetAt)
	case errors.As(err, &rl):
		c.Limits.resetCheckStillLimited(rc.Pool)
		log.Info("reset check: still limited", "msg", rl.Message)
	case errors.As(err, &te) || ctx.Err() != nil:
		// A network blip or timeout says nothing about the limit. Keep the
		// account blocked and try again on the next slot.
		c.Limits.resetCheckStillLimited(rc.Pool)
		log.Warn("reset check: probe failed, retrying later", "err", err)
	default:
		// The provider answered: a clean run, or a non-limit outcome such as
		// the one-turn cap or an auth error, which the next task run handles
		// through its own path. Either way the limit is gone.
		c.Limits.Unblock(rc.Pool)
		log.Info("reset check: provider usable again, unblocking", "probe_err", err)
	}
}

// probe runs one minimal prompt as cfg in an empty scratch directory.
func (c *ResetChecker) probe(ctx context.Context, cfg AgentConfig) error {
	prov := c.NewProvider(cfg)
	if prov == nil {
		return errors.New("provider unavailable")
	}
	dir, err := os.MkdirTemp("", "ate-reset-check-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	// The CLI may run as the unprivileged agent user.
	if err := os.Chmod(dir, 0o777); err != nil {
		return err
	}

	timeout := c.Timeout
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}
	pcfg := cfg
	pcfg.SystemPrompt = ""
	pcfg.MaxTurns = 1
	pcfg.TimeoutSecs = int64(timeout / time.Second)
	pcfg.MaxCostUSD = 0
	pcfg.ResumeSessions = false
	pcfg.SubtasksEnabled = false
	pcfg.EnabledPlugins = nil
	pcfg.EnabledMCPServers = nil

	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	id := uuid.NewString()
	logCh := make(chan LogEntry, 64)
	drained := make(chan struct{})
	go func() {
		for range logCh {
		}
		close(drained)
	}()
	_, err = prov.Run(runCtx, RunInput{
		RunID:       "reset-check-" + id,
		Task:        Task{ID: "reset-check-" + id, Title: resetCheckPrompt, Description: resetCheckPrompt},
		AgentConfig: pcfg,
		RepoPath:    dir,
	}, logCh)
	close(logCh)
	<-drained
	return err
}
