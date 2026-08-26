package agent

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

// fakeLLMDetector is a minimal, hermetic LLMDetector for unit tests in this
// package — never shells out to a real CLI.
type fakeLLMDetector struct {
	guesses []LanguageGuess
	err     error
	calls   int
}

func (f *fakeLLMDetector) DetectLanguages(_ context.Context, _ LLMDetectInput) ([]LanguageGuess, error) {
	f.calls++
	return f.guesses, f.err
}

func TestNeedsLLMFallback(t *testing.T) {
	cases := []struct {
		name string
		in   []LanguageSuggestion
		want bool
	}{
		{"empty", nil, true},
		{"clean exact version", []LanguageSuggestion{{ID: "go", Version: "1.26"}}, false},
		{"one ambiguous among clean", []LanguageSuggestion{
			{ID: "go", Version: "1.26"},
			{ID: "node", Version: "20", Ambiguous: true},
		}, true},
		{"all clean, multiple", []LanguageSuggestion{
			{ID: "go", Version: "1.26"},
			{ID: "python", Version: "3.12"},
		}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := needsLLMFallback(tc.in); got != tc.want {
				t.Errorf("needsLLMFallback(%+v) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestDetectLanguagesWithFallback_CleanScan_SkipsLLM(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module example.com/foo\n\ngo 1.26\n")

	fake := &fakeLLMDetector{guesses: []LanguageGuess{{ID: "node", Version: "20"}}}
	suggestions, usedLLM, err := DetectLanguagesWithFallback(context.Background(), dir, fake)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if usedLLM {
		t.Errorf("usedLLM = true, want false")
	}
	if fake.calls != 0 {
		t.Errorf("LLM invoked %d times, want 0", fake.calls)
	}
	if len(suggestions) != 1 || suggestions[0].ID != "go" {
		t.Errorf("unexpected suggestions: %+v", suggestions)
	}
}

func TestDetectLanguagesWithFallback_NilDetector_SkipsSilently(t *testing.T) {
	dir := t.TempDir() // empty repo -> would need LLM, but none configured
	suggestions, usedLLM, err := DetectLanguagesWithFallback(context.Background(), dir, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if usedLLM {
		t.Errorf("usedLLM = true, want false")
	}
	if len(suggestions) != 0 {
		t.Errorf("expected no suggestions, got %+v", suggestions)
	}
}

func TestDetectLanguagesWithFallback_LLMError_Degrades(t *testing.T) {
	dir := t.TempDir()
	fake := &fakeLLMDetector{err: errors.New("boom")}
	suggestions, usedLLM, err := DetectLanguagesWithFallback(context.Background(), dir, fake)
	if err != nil {
		t.Fatalf("LLM failure must not surface as an endpoint error, got: %v", err)
	}
	if usedLLM {
		t.Errorf("usedLLM = true, want false")
	}
	if len(suggestions) != 0 {
		t.Errorf("expected empty (scan) result, got %+v", suggestions)
	}
	if fake.calls != 1 {
		t.Errorf("LLM invoked %d times, want 1", fake.calls)
	}
}

func TestDetectLanguagesWithFallback_ScanError_Propagates(t *testing.T) {
	// A repoPath that doesn't exist makes DetectLanguages itself fail
	// (root-level read error) — this is the one case that must not be
	// swallowed, since there is no scan result to fall back to.
	_, _, err := DetectLanguagesWithFallback(context.Background(), "/nonexistent/path/does/not/exist", nil)
	if err == nil {
		t.Fatalf("expected an error for an unreadable repo path")
	}
}

func TestValidateLLMGuesses(t *testing.T) {
	cases := []struct {
		name    string
		guesses []LanguageGuess
		wantLen int
	}{
		{"valid single", []LanguageGuess{{ID: "go", Version: "1.26"}}, 1},
		{"empty input", nil, 0},
		{"unknown id", []LanguageGuess{{ID: "cobol", Version: "1985"}}, 0},
		{"flag as id", []LanguageGuess{{ID: "--privileged", Version: "1"}}, 0},
		{"shell injection in version", []LanguageGuess{{ID: "go", Version: "; rm -rf /"}}, 0},
		{"empty version", []LanguageGuess{{ID: "go", Version: ""}}, 0},
		{"duplicate id", []LanguageGuess{{ID: "go", Version: "1.26"}, {ID: "go", Version: "1.25"}}, 0},
		{"one hostile entry taints the whole batch", []LanguageGuess{
			{ID: "go", Version: "1.26"},
			{ID: "cobol", Version: "1985"},
		}, 0},
		{"version too long", []LanguageGuess{{ID: "go", Version: fmt.Sprintf("%033d", 0)}}, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := validateLLMGuesses(tc.guesses)
			if len(got) != tc.wantLen {
				t.Errorf("validateLLMGuesses(%+v) = %+v, want len %d", tc.guesses, got, tc.wantLen)
			}
		})
	}
}

func TestDetectLanguagesWithFallback_HostileOutput_DropsAndDegrades(t *testing.T) {
	dir := t.TempDir() // empty scan -> LLM path reached
	fake := &fakeLLMDetector{guesses: []LanguageGuess{{ID: "--privileged", Version: "; rm -rf /"}}}
	suggestions, usedLLM, err := DetectLanguagesWithFallback(context.Background(), dir, fake)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if usedLLM {
		t.Errorf("usedLLM = true, want false — hostile output must not count as used")
	}
	if len(suggestions) != 0 {
		t.Errorf("hostile output leaked into suggestions: %+v", suggestions)
	}
}

func TestMergeSuggestions_LLMFillsGapWithoutOverridingConfidentScan(t *testing.T) {
	scanned := []LanguageSuggestion{
		{ID: "go", Version: "1.26", Source: "go.mod"},                        // confident — must not be overridden
		{ID: "node", Version: "20", Source: "package.json", Ambiguous: true}, // ambiguous — llm may refine
	}
	llmLangs := []RuntimeLanguage{
		{ID: "go", Version: "1.99"},     // must be ignored
		{ID: "node", Version: "20.1"},   // refines the ambiguous scan entry
		{ID: "python", Version: "3.12"}, // new — scan had no evidence at all
	}
	merged := mergeSuggestions(scanned, llmLangs)

	byID := make(map[string]LanguageSuggestion, len(merged))
	for _, s := range merged {
		byID[s.ID] = s
	}
	if byID["go"].Version != "1.26" || byID["go"].Source != "go.mod" {
		t.Errorf("go suggestion was overridden by LLM guess: %+v", byID["go"])
	}
	if byID["node"].Version != "20.1" || byID["node"].Source != "claude" {
		t.Errorf("node suggestion not refined by LLM: %+v", byID["node"])
	}
	if byID["python"].Version != "3.12" || byID["python"].Source != "claude" {
		t.Errorf("python suggestion missing/wrong: %+v", byID["python"])
	}
}

func TestBuildLLMDetectInput_BoundsSize(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module example.com/foo\n\ngo 1.26\n")
	scanned := []LanguageSuggestion{{ID: "go", Version: "1.26", Source: "go.mod"}}

	input := buildLLMDetectInput(dir, scanned)
	if len(input.ManifestContents) != 1 {
		t.Fatalf("expected 1 manifest content, got %d", len(input.ManifestContents))
	}
	content, ok := input.ManifestContents["go.mod"]
	if !ok {
		t.Fatalf("expected go.mod content present")
	}
	if len(content) > llmDetectMaxTotalBytes {
		t.Errorf("manifest content exceeds cap: %d > %d", len(content), llmDetectMaxTotalBytes)
	}
	if len(input.DirListing) > llmDetectMaxDirEntries {
		t.Errorf("dir listing exceeds cap: %d > %d", len(input.DirListing), llmDetectMaxDirEntries)
	}
}
