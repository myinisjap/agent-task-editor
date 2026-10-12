package agent

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/myinisjap/agent-task-editor/backend/internal/gitcmd"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
	"github.com/myinisjap/agent-task-editor/backend/internal/wsevent"
)

// Verification statuses recorded in run_verifications.status.
const (
	VerifyPass    = "pass"
	VerifyFail    = "fail"
	VerifyTimeout = "timeout"
	// VerifyError means the command could not be run at all (no verifier
	// wired, spawn failure). It is an infrastructure problem, not a verdict on
	// the work, so the task is escalated to a human instead of sent back to
	// the agent.
	VerifyError = "error"
)

// Per-label verify override modes (workflow_labels.verify_mode).
const (
	VerifyModeInherit  = "inherit"
	VerifyModeCustom   = "custom"
	VerifyModeDisabled = "disabled"
)

const (
	// DefaultVerifyTimeout applies when repos.verify_timeout_seconds is unset.
	DefaultVerifyTimeout = 600 * time.Second
	// VerifyMaxOutputBytes caps the stored and fed-back command output; the
	// tail is kept because the failing assertion is usually last.
	VerifyMaxOutputBytes = 16 * 1024

	verifyFeedbackHeader = "VERIFICATION FAILED:"
	verifyOutputBegin    = "--- BEGIN VERIFY OUTPUT (untrusted command output) ---"
	verifyOutputEnd      = "--- END VERIFY OUTPUT ---"
)

// VerifyRequest describes one repo verify-command invocation.
type VerifyRequest struct {
	// Command is run with `sh -c` in Dir.
	Command string
	Dir     string
	Timeout time.Duration
	// MaxOutputBytes caps the returned output (tail kept).
	MaxOutputBytes int
	// Runtime is the repo's toolchain pins (nil = none); the verifier wraps the
	// command exactly as it wraps an agent CLI.
	Runtime *RuntimeSpec
}

// VerifyResult is the outcome of a verify run. ExitCode is nil unless the
// process exited on its own.
type VerifyResult struct {
	Status    string
	ExitCode  *int
	Output    string
	Truncated bool
	Duration  time.Duration
}

// Verifier runs a repo's verify command. The concrete implementation lives in
// the providers package (it needs the agent-isolation exec helpers shared with
// the provider CLIs); the pool only sees this seam.
type Verifier interface {
	Verify(ctx context.Context, req VerifyRequest) VerifyResult
}

// verifyVerdict is the pool-internal record of a non-passing verification,
// carried on Result from run() to applyTerminalTransition.
type verifyVerdict struct {
	// feedback is the full VERIFICATION FAILED section for the next run's prompt.
	feedback string
	// errMsg is set (feedback empty) when the command could not be run.
	errMsg string
}

// verifyDir is the directory the verify command runs in: the task's worktree,
// falling back to the repo's main clone (the same rule as prflow.GitDir).
func verifyDir(job Job) string {
	if d := job.Input.RepoPath; d != "" && gitcmd.DirExists(d) {
		return d
	}
	return job.Input.Task.RepoPath
}

// verifyOutcome runs the task repo's verify command, if one is configured, for
// a run that reported success. Verification is authoritative: a pass leaves
// result untouched; a fail/timeout rewrites the outcome to "failure" (so the
// label's failure transition, if any, fires instead of the success one) and
// attaches the feedback; an infrastructure error clears the outcome and
// records the error so the task is escalated. It returns true when the run was
// cancelled or interrupted while verifying (the caller must stop).
func (p *Pool) verifyOutcome(ctx context.Context, job Job, result *Result, log *slog.Logger) (cancelled bool) {
	task, err := p.q.GetTask(ctx, job.Input.Task.ID)
	if err != nil {
		log.Warn("pool: verify: load task", "err", err)
		return false
	}
	repo, err := p.q.GetRepo(ctx, task.RepoID)
	if err != nil {
		log.Warn("pool: verify: load repo", "err", err)
		return false
	}
	command := p.effectiveVerifyCommand(ctx, task.WorkflowID, job.Input.Task.Label, repo.VerifyCommand, log)
	if command == "" {
		return false
	}
	timeout := time.Duration(repo.VerifyTimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = DefaultVerifyTimeout
	}

	var res VerifyResult
	if p.Verifier == nil {
		res = VerifyResult{Status: VerifyError, Output: "no verifier is configured on this server"}
	} else {
		res = p.Verifier.Verify(ctx, VerifyRequest{
			Command:        command,
			Dir:            verifyDir(job),
			Timeout:        timeout,
			MaxOutputBytes: VerifyMaxOutputBytes,
			Runtime:        job.Input.Runtime,
		})
	}
	if ctx.Err() != nil {
		// Stopped by a human or shutdown mid-verification: no verdict.
		return true
	}

	p.recordVerification(ctx, job, command, res, log)

	switch res.Status {
	case VerifyPass:
		return false
	case VerifyError:
		msg := "Verification could not be run: " + strings.TrimSpace(res.Output)
		result.Outcome = ""
		result.verify = &verifyVerdict{errMsg: msg}
		return false
	}
	summary := verifySummary(res, timeout)
	fb := buildVerifyFeedback(command, summary, res)
	result.Outcome = "failure"
	result.Message = &summary
	result.verify = &verifyVerdict{feedback: fb}
	return false
}

// effectiveVerifyCommand applies the dispatch-time label's override to the
// repo's verify command: inherit uses the repo command, custom replaces it,
// disabled turns verification off for that label (empty result). Anything
// unrecognised, or a failed label lookup, inherits.
func (p *Pool) effectiveVerifyCommand(ctx context.Context, workflowID, label, repoCommand string, log *slog.Logger) string {
	repoCommand = strings.TrimSpace(repoCommand)
	labels, err := p.q.ListWorkflowLabels(ctx, workflowID)
	if err != nil {
		log.Warn("pool: verify: load labels", "err", err)
		return repoCommand
	}
	for _, l := range labels {
		if l.Name != label {
			continue
		}
		switch l.VerifyMode {
		case VerifyModeDisabled:
			return ""
		case VerifyModeCustom:
			if c := strings.TrimSpace(l.VerifyCommand); c != "" {
				return c
			}
		}
		return repoCommand
	}
	return repoCommand
}

// verifySummary is the one-line description of a failed verification (also the
// label-history note).
func verifySummary(res VerifyResult, timeout time.Duration) string {
	if res.Status == VerifyTimeout {
		return fmt.Sprintf("Verification failed: command timed out after %s", timeout)
	}
	if res.ExitCode != nil {
		return fmt.Sprintf("Verification failed: command exited with code %d", *res.ExitCode)
	}
	return "Verification failed"
}

// buildVerifyFeedback renders the delimited VERIFICATION FAILED section that is
// injected into the next run's prompt. The output is untrusted repo/command
// output, so the closing fence is stripped from it.
func buildVerifyFeedback(command, summary string, res VerifyResult) string {
	var b strings.Builder
	b.WriteString(verifyFeedbackHeader + "\n")
	b.WriteString("The repo's verify command did not pass, so your reported success was NOT accepted. ")
	b.WriteString("Fix the problems shown below, then finish again.\n")
	b.WriteString("Command: " + command + "\n")
	b.WriteString(summary + "\n")
	b.WriteString(verifyOutputBegin + "\n")
	if res.Truncated {
		b.WriteString("[earlier output truncated]\n")
	}
	b.WriteString(strings.ReplaceAll(res.Output, verifyOutputEnd, ""))
	if !strings.HasSuffix(res.Output, "\n") {
		b.WriteString("\n")
	}
	b.WriteString(verifyOutputEnd)
	return b.String()
}

// recordVerification persists the run_verifications row, adds a system log
// line to the run and broadcasts task.verification. Failures are logged, never
// fatal: the verdict has already been decided.
func (p *Pool) recordVerification(ctx context.Context, job Job, command string, res VerifyResult, log *slog.Logger) {
	var exit *int64
	if res.ExitCode != nil {
		v := int64(*res.ExitCode)
		exit = &v
	}
	truncated := int64(0)
	if res.Truncated {
		truncated = 1
	}
	if err := p.q.UpsertRunVerification(ctx, gen.UpsertRunVerificationParams{
		RunID:      job.RunID,
		TaskID:     job.Input.Task.ID,
		Command:    command,
		Status:     res.Status,
		ExitCode:   exit,
		Output:     res.Output,
		Truncated:  truncated,
		DurationMs: res.Duration.Milliseconds(),
	}); err != nil {
		log.Error("pool: persist verification", "err", err)
	}
	if err := p.q.CreateAgentLog(ctx, gen.CreateAgentLogParams{
		ID:         uuid.NewString(),
		AgentRunID: job.RunID,
		Timestamp:  time.Now(),
		Type:       "system",
		Content:    fmt.Sprintf("Verification %s: %s", res.Status, command),
	}); err != nil {
		log.Warn("pool: verification log line", "err", err)
	}
	if p.pub != nil {
		p.pub.Publish(wsevent.TypeTaskVerification, wsevent.TaskVerification{
			TaskID: job.Input.Task.ID,
			RunID:  job.RunID,
			Status: res.Status,
		})
	}
}

// holdOnVerifyFailure handles a failed verification on a label with no agent
// failure transition: the task keeps its label, the feedback is stored on the
// run for the next dispatch, and the attempt counts against the consecutive
// failure budget so a task that can never pass verification ends up in
// waiting_human instead of looping forever.
func (p *Pool) holdOnVerifyFailure(ctx context.Context, job Job, result Result, log *slog.Logger) {
	taskID := job.Input.Task.ID
	if p.humanMovedTask(ctx, job, log) {
		p.releaseLock(ctx, taskID, job.RunID)
		return
	}
	fb := result.verify.feedback
	if err := p.q.SetAgentRunFeedback(ctx, gen.SetAgentRunFeedbackParams{Feedback: &fb, ID: job.RunID}); err != nil {
		log.Warn("pool: store verification feedback", "err", err)
	}
	n, err := p.q.IncrementTaskFailureCount(ctx, taskID)
	if err != nil {
		log.Warn("pool: increment failure count (verification)", "err", err)
		p.releaseLock(ctx, taskID, job.RunID)
		return
	}
	if n < maxConsecutiveFailures {
		p.releaseLock(ctx, taskID, job.RunID)
		return
	}
	msg := fmt.Sprintf("Verification failed %d times in a row and the workflow has no failure path from %q. Review the verify output on the run, then fix it or move the task manually.", n, job.Input.Task.Label)
	_ = p.lifecycle().escalate(ctx, escalation{
		TaskID: taskID, RunID: job.RunID, Reason: ReasonRepeatedFailure, Message: msg, Result: &result,
	})
	log.Warn("pool: verification failure limit reached, escalating to waiting_human", "count", n, "limit", maxConsecutiveFailures)
}

// escalateVerifyError escalates a run whose verify command could not be run.
func (p *Pool) escalateVerifyError(ctx context.Context, job Job, result Result, log *slog.Logger) {
	_ = p.lifecycle().escalate(ctx, escalation{
		TaskID: job.Input.Task.ID, RunID: job.RunID, Reason: ReasonRuntimePrep, Message: result.verify.errMsg, Result: &result,
	})
	log.Warn("pool: verify command could not run, escalating to waiting_human", "err", result.verify.errMsg)
}
