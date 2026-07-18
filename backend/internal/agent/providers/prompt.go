package providers

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/myinisjap/agent-task-editor/backend/internal/agent"
)

func buildPrompt(input agent.RunInput) string {
	var b strings.Builder
	writeHumanReplySection(&b, input)
	writeSubtaskConflictSection(&b, input)
	writeFeedbackSection(&b, input)
	writeReviewCommentsSection(&b, input)
	if input.PriorPlan != nil && *input.PriorPlan != "" {
		b.WriteString("NOTES FROM PRIOR AGENT:\n")
		b.WriteString(*input.PriorPlan)
		b.WriteString("\n\n---\n\n")
	}
	fmt.Fprintf(&b, "Task: %s\n\n", input.Task.Title)
	if input.Task.Description != "" {
		b.WriteString(input.Task.Description)
	}
	if len(input.Task.Attachments) > 0 {
		b.WriteString("\n\nATTACHED IMAGES (available in .task_attachments/ within the repo):\n")
		for _, rel := range input.Task.Attachments {
			fmt.Fprintf(&b, "- .task_attachments/%s\n", filepath.Base(rel))
		}
	}
	return b.String()
}

// buildResumePrompt is the prompt for a run that resumes a prior provider
// session (claude --resume). The resumed conversation already contains the
// task context (title, description, notes, prior work) as its own turns, so
// only the *new* information is sent as the next message.
func buildResumePrompt(input agent.RunInput) string {
	var b strings.Builder
	writeHumanReplySection(&b, input)
	writeSubtaskConflictSection(&b, input)
	writeFeedbackSection(&b, input)
	writeReviewCommentsSection(&b, input)
	if b.Len() == 0 {
		fmt.Fprintf(&b, "Continue working on the task: %s\n\n", input.Task.Title)
	}
	b.WriteString("This session was resumed from your previous run on this task — the conversation above is your own prior work. Continue from where you left off rather than starting over.")
	return b.String()
}

// writeHumanReplySection renders the human's answer to the agent's
// request_human question, when a reply started this run.
func writeHumanReplySection(b *strings.Builder, input agent.RunInput) {
	if input.HumanReply == nil || *input.HumanReply == "" {
		return
	}
	b.WriteString("RESPONSE FROM HUMAN (answering your request for help):\n")
	b.WriteString(*input.HumanReply)
	b.WriteString("\n\n---\n\n")
}

// writeSubtaskConflictSection renders subtask branches that conflicted when
// merging back into this parent's branch, so the parent's work agent resolves
// them (git merge the named branches and fix the conflicting files).
func writeSubtaskConflictSection(b *strings.Builder, input agent.RunInput) {
	if input.SubtaskConflicts == nil || *input.SubtaskConflicts == "" {
		return
	}
	b.WriteString("SUBTASK MERGE CONFLICTS (resolve these first):\n")
	b.WriteString(*input.SubtaskConflicts)
	b.WriteString("\n\n---\n\n")
}

func writeFeedbackSection(b *strings.Builder, input agent.RunInput) {
	if input.Feedback == nil || *input.Feedback == "" {
		return
	}
	b.WriteString("FEEDBACK FROM PRIOR REVIEW:\n")
	b.WriteString(*input.Feedback)
	b.WriteString("\n\n---\n\n")
}

func writeReviewCommentsSection(b *strings.Builder, input agent.RunInput) {
	if len(input.OpenReviewComments) == 0 {
		return
	}
	b.WriteString("OPEN REVIEW COMMENTS (inline comments a human left on your branch's diff — address every one):\n\n")
	for i, c := range input.OpenReviewComments {
		lineRef := fmt.Sprintf("line %d", c.StartLine)
		if c.EndLine != c.StartLine {
			lineRef = fmt.Sprintf("lines %d-%d", c.StartLine, c.EndLine)
		}
		fmt.Fprintf(b, "%d. [comment_id: %s] %s (%s):\n", i+1, c.ID, c.FilePath, lineRef)
		if c.QuotedText != "" {
			b.WriteString("```\n")
			b.WriteString(c.QuotedText)
			b.WriteString("\n```\n")
		}
		fmt.Fprintf(b, "→ %s\n\n", c.Body)
	}
	b.WriteString("After addressing each comment, call mcp__task-editor__resolve_comment with its comment_id and a one-line note describing your fix. If that tool is unavailable, list each addressed comment_id in your task notes instead.\n\n---\n\n")
}

func buildSystemPrompt(input agent.RunInput) string {
	base := input.AgentConfig.SystemPrompt
	if base == "" {
		base = "You are an expert software engineer. Complete the assigned task thoroughly and carefully."
	}
	// Dynamically inject the repo working directory so the agent always knows where to work.
	var dirLine string
	if input.RepoPath != "" {
		dirLine = fmt.Sprintf("\n\nThe repository you are working on is located at: %s\nAll file operations should be performed relative to this directory.", input.RepoPath)
	}
	suffix := "\n\nIf the prompt contains an \"OPEN REVIEW COMMENTS\" section, treat each comment as a code-review finding on your branch: address every one, then call mcp__task-editor__resolve_comment with the comment's comment_id and a one-line note describing the fix.\n\nIf the prompt contains a \"NOTES FROM PRIOR AGENT\" section, read it carefully before starting — it contains context, plans, and decisions from previous agents in this workflow.\n\nBefore calling mcp__task-editor__signal_complete, call mcp__task-editor__update_task_notes with a concise summary of what you did, what decisions you made, and any context the next agent will need. If prior notes exist (\"NOTES FROM PRIOR AGENT\" was present), use append:true to preserve them. This is how agents hand off state to each other — always do it.\n\nWhen your work is complete, call the mcp__task-editor__signal_complete tool with outcome='success' if the work succeeded or outcome='failure' if it did not. If the MCP tool is unavailable, end your final response with exactly: OUTCOME: success  or  OUTCOME: failure"
	return base + dirLine + suffix
}
