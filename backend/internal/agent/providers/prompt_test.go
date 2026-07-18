package providers

import (
	"strings"
	"testing"

	"github.com/myinisjap/agent-task-editor/backend/internal/agent"
)

// TestBuildPrompt_FeedbackInjected verifies a human rejection note (carried as
// RunInput.Feedback) is rendered at the top of the agent prompt — the read side
// of the reject-feedback round-trip.
func TestBuildPrompt_FeedbackInjected(t *testing.T) {
	fb := "needs more tests"
	out := buildPrompt(agent.RunInput{
		Task:     agent.Task{Title: "Do the thing"},
		Feedback: &fb,
	})
	if !strings.HasPrefix(out, "FEEDBACK FROM PRIOR REVIEW:\n"+fb) {
		t.Fatalf("feedback not at top of prompt; got:\n%s", out)
	}
}

// TestBuildPrompt_OpenReviewCommentsInjected verifies that open inline diff
// review comments are rendered into the prompt with their comment_id, file
// and line anchors, quoted diff text, and the resolve_comment instruction.
func TestBuildPrompt_OpenReviewCommentsInjected(t *testing.T) {
	out := buildPrompt(agent.RunInput{
		Task: agent.Task{Title: "Do the thing"},
		OpenReviewComments: []agent.ReviewComment{
			{ID: "c-1", FilePath: "main.go", Side: "new", StartLine: 10, EndLine: 12, QuotedText: "x := 1", Body: "use the existing helper"},
			{ID: "c-2", FilePath: "util.go", Side: "new", StartLine: 5, EndLine: 5, Body: "typo in comment"},
		},
	})
	for _, want := range []string{
		"OPEN REVIEW COMMENTS",
		"[comment_id: c-1] main.go (lines 10-12)",
		"x := 1",
		"→ use the existing helper",
		"[comment_id: c-2] util.go (line 5)",
		"mcp__task-editor__resolve_comment",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("prompt missing %q; got:\n%s", want, out)
		}
	}
}

// TestBuildPrompt_NoReviewComments verifies the section is absent when there
// are no open comments.
func TestBuildPrompt_NoReviewComments(t *testing.T) {
	out := buildPrompt(agent.RunInput{Task: agent.Task{Title: "Do the thing"}})
	if strings.Contains(out, "OPEN REVIEW COMMENTS") {
		t.Fatalf("unexpected review comments section in prompt:\n%s", out)
	}
}

// TestBuildResumePrompt_NoNewInfo verifies the resume prompt degrades to a
// plain continuation instruction when there is no reply/feedback/comments.
func TestBuildResumePrompt_NoNewInfo(t *testing.T) {
	p := buildResumePrompt(agent.RunInput{Task: agent.Task{Title: "Fix the bug"}, ResumeSessionID: "sess-1"})
	if !strings.Contains(p, "Continue working on the task: Fix the bug") {
		t.Errorf("expected continuation line, got %q", p)
	}
}
