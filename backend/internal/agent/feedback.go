package agent

import (
	"context"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
	"log/slog"
	"strings"
)

// consumeInheritedFeedback drops the feedback this run was handed at dispatch
// from its own row once it succeeded, so the next dispatch (which copies
// current_agent_run_id's feedback) does not re-send it. Feedback appended
// during the run (ghsync) is kept; a concurrent overwrite (human reject) wins.
func (p *Pool) consumeInheritedFeedback(ctx context.Context, job Job, log *slog.Logger) {
	if job.Input.Feedback == nil || strings.TrimSpace(*job.Input.Feedback) == "" {
		return
	}
	run, err := p.q.GetAgentRun(ctx, job.RunID)
	if err != nil {
		log.Warn("consume feedback: load run failed", "err", err)
		return
	}
	if run.Feedback == nil {
		return
	}
	cur := *run.Feedback
	inh := strings.TrimSpace(*job.Input.Feedback)
	var next *string
	switch {
	case strings.TrimSpace(cur) == inh:
		next = nil
	case strings.HasPrefix(cur, inh+"\n\n"):
		rest := strings.TrimPrefix(cur, inh+"\n\n")
		if strings.TrimSpace(rest) != "" {
			next = &rest
		}
	default:
		return
	}
	n, err := p.q.ReplaceAgentRunFeedbackIfUnchanged(ctx, gen.ReplaceAgentRunFeedbackIfUnchangedParams{
		Feedback:   next,
		ID:         job.RunID,
		Feedback_2: &cur,
	})
	if err != nil {
		log.Warn("consume feedback: update failed", "err", err)
		return
	}
	if n == 0 {
		log.Info("feedback changed concurrently; leaving it")
	}
}
