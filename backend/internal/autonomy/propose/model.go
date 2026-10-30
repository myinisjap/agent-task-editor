package propose

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/myinisjap/agent-task-editor/backend/internal/agent"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

// AgentConfigFor builds the run config for a provider config: its provider,
// model and env, with nothing else enabled.
func AgentConfigFor(pc gen.ProviderConfig) agent.AgentConfig {
	var env map[string]string
	_ = json.Unmarshal([]byte(pc.Env), &env)
	if env == nil {
		env = map[string]string{}
	}
	return agent.AgentConfig{
		ID:        "autonomy-proposals",
		Name:      "Autonomy rule proposals",
		Provider:  pc.Provider,
		Model:     pc.Model,
		UsagePool: agent.UsagePoolKey(pc.ID, pc.Provider, env),
		Env:       env,
	}
}

// ProviderModel answers a prompt with one run of a provider CLI, outside any
// task, in an empty scratch directory (like the rate-limit reset check).
// Shell commands are denied; the prompt asks for no tools at all.
type ProviderModel struct {
	Provider agent.Provider
	Config   agent.AgentConfig
}

// Complete runs the CLI once and returns every string it printed, so the
// answer block can be found whatever the provider's output format.
func (m ProviderModel) Complete(ctx context.Context, prompt string) (Completion, error) {
	if m.Provider == nil {
		return Completion{}, errors.New("provider unavailable")
	}
	dir, err := os.MkdirTemp("", "ate-autonomy-propose-")
	if err != nil {
		return Completion{}, err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	// The CLI may run as the unprivileged agent user.
	if err := os.Chmod(dir, 0o777); err != nil {
		return Completion{}, err
	}
	cfg := m.Config
	cfg.SystemPrompt = SystemPrompt
	cfg.MaxTurns = 3
	cfg.TimeoutSecs = int64(ModelTimeout / time.Second)
	cfg.MaxCostUSD = 0
	cfg.ResumeSessions = false
	cfg.SubtasksEnabled = false
	cfg.EnabledPlugins = nil
	cfg.EnabledMCPServers = nil
	cfg.CommandDenylist = []string{"*"}

	id := uuid.NewString()
	logCh := make(chan agent.LogEntry, 64)
	var text strings.Builder
	drained := make(chan struct{})
	go func() {
		for e := range logCh {
			if e.Type == agent.LogSystem && !strings.HasPrefix(strings.TrimSpace(e.Content), "{") {
				continue
			}
			appendStrings(&text, e.Content)
		}
		close(drained)
	}()
	res, err := m.Provider.Run(ctx, agent.RunInput{
		RunID:       "autonomy-propose-" + id,
		Task:        agent.Task{ID: "autonomy-propose-" + id, Title: "Propose ask rules", Description: prompt},
		AgentConfig: cfg,
		RepoPath:    dir,
	}, logCh)
	close(logCh)
	<-drained
	comp := Completion{Text: text.String(), CostUSD: res.CostUSD, InputTokens: res.InputTokens, OutputTokens: res.OutputTokens}
	if err != nil {
		return comp, err
	}
	if res.Status == "failed" && !strings.Contains(comp.Text, outputEnd) {
		return comp, errors.New("provider run failed")
	}
	return comp, nil
}

// appendStrings writes line to b; a JSON line contributes every string value
// in it instead (stream-json providers log raw events with the prose inside).
func appendStrings(b *strings.Builder, line string) {
	var v any
	if err := json.Unmarshal([]byte(line), &v); err != nil {
		b.WriteString(line)
		b.WriteString("\n")
		return
	}
	var walk func(any)
	walk = func(x any) {
		switch t := x.(type) {
		case string:
			b.WriteString(t)
			b.WriteString("\n")
		case []any:
			for _, e := range t {
				walk(e)
			}
		case map[string]any:
			for _, e := range t {
				walk(e)
			}
		}
	}
	walk(v)
}
