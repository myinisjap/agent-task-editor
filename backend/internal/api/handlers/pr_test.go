package handlers

import (
	"strings"
	"testing"

	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

func TestBuildPRBody(t *testing.T) {
	task := gen.Task{
		Title:       "Fix login redirect",
		Description: "Users land on a blank page after login.",
		AgentNotes:  "Reordered the redirect guard.",
	}
	body := buildPRBody(task, []string{"Fix redirect order", "Add regression test"})

	for _, want := range []string{
		"Users land on a blank page",
		"### What changed",
		"Reordered the redirect guard.",
		"### Commits",
		"- Fix redirect order",
		"- Add regression test",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("body missing %q:\n%s", want, body)
		}
	}

	// Empty sections are omitted, not rendered as empty headers.
	bare := buildPRBody(gen.Task{Title: "x"}, nil)
	if strings.Contains(bare, "### What changed") || strings.Contains(bare, "### Commits") {
		t.Fatalf("bare body should omit empty sections:\n%q", bare)
	}
}
