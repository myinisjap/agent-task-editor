package providers

import (
	"strings"
	"testing"

	"github.com/myinisjap/agent-task-editor/backend/internal/agent"
)

func TestExtractJSONArray(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"bare array", `[{"id":"go","version":"1.26"}]`, `[{"id":"go","version":"1.26"}]`},
		{"wrapped in prose", "Sure, here you go:\n[{\"id\":\"go\",\"version\":\"1.26\"}]\nHope that helps!", `[{"id":"go","version":"1.26"}]`},
		{"markdown fence", "```json\n[]\n```", `[]`},
		{"no brackets", "I don't know", ""},
		{"empty", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := extractJSONArray(tc.in); got != tc.want {
				t.Errorf("extractJSONArray(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestParseDetectLangOutput_ValidEnvelope(t *testing.T) {
	raw := []byte(`{"result": "[{\"id\":\"go\",\"version\":\"1.26\"}]"}`)
	guesses, err := parseDetectLangOutput(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(guesses) != 1 || guesses[0].ID != "go" || guesses[0].Version != "1.26" {
		t.Errorf("unexpected guesses: %+v", guesses)
	}
}

func TestParseDetectLangOutput_EmptyArray(t *testing.T) {
	raw := []byte(`{"result": "[]"}`)
	guesses, err := parseDetectLangOutput(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(guesses) != 0 {
		t.Errorf("expected no guesses, got %+v", guesses)
	}
}

func TestParseDetectLangOutput_NotJSON(t *testing.T) {
	raw := []byte("this is not json at all")
	if _, err := parseDetectLangOutput(raw); err == nil {
		t.Fatalf("expected an error for non-JSON envelope")
	}
}

func TestParseDetectLangOutput_TruncatedEnvelope(t *testing.T) {
	raw := []byte(`{"result": "[{"id":"go"`)
	if _, err := parseDetectLangOutput(raw); err == nil {
		t.Fatalf("expected an error for truncated JSON")
	}
}

func TestParseDetectLangOutput_WrongShape(t *testing.T) {
	// "result" holds a JSON object, not an array of {id,version}.
	raw := []byte(`{"result": "{\"id\":\"go\"}"}`)
	if _, err := parseDetectLangOutput(raw); err == nil {
		t.Fatalf("expected an error when the answer isn't a JSON array")
	}
}

func TestParseDetectLangOutput_ProseWrappedArray(t *testing.T) {
	raw := []byte(`{"result": "Here is my answer: [{\"id\":\"node\",\"version\":\"20\"}] done."}`)
	guesses, err := parseDetectLangOutput(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(guesses) != 1 || guesses[0].ID != "node" {
		t.Errorf("unexpected guesses: %+v", guesses)
	}
}

func TestBuildDetectLangPrompt_ConstrainsToAllowlistAndCarriesContent(t *testing.T) {
	input := agent.LLMDetectInput{
		ManifestContents: map[string]string{"go.mod": "module x\n\ngo 1.26\n"},
		DirListing:       []string{"go.mod", "main.go"},
	}
	prompt := buildDetectLangPrompt(input)

	for _, id := range claudeDetectLangAllowedIDs {
		if !strings.Contains(prompt, id) {
			t.Errorf("prompt missing allowlisted id %q", id)
		}
	}
	if !strings.Contains(prompt, "go.mod") {
		t.Errorf("prompt missing manifest filename")
	}
	if !strings.Contains(prompt, "module x") {
		t.Errorf("prompt missing manifest content")
	}
	if !strings.Contains(prompt, "[]") {
		t.Errorf("prompt does not instruct returning [] when unsure")
	}
	if !strings.Contains(prompt, "main.go") {
		t.Errorf("prompt missing directory listing entry")
	}
}

func TestClaudeLanguageDetector_Binary_DefaultsToClaude(t *testing.T) {
	d := &ClaudeLanguageDetector{}
	if got := d.binary(); got != "claude" {
		t.Errorf("binary() = %q, want %q", got, "claude")
	}
	d.BinaryPath = "/custom/path/claude"
	if got := d.binary(); got != "/custom/path/claude" {
		t.Errorf("binary() = %q, want override", got)
	}
}
