package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/myinisjap/agent-task-editor/backend/internal/agent"
)

// claudeDetectLangTimeout bounds the one-shot detection call — short because
// this is a single classification prompt, not an agentic coding session, and
// a hung/slow CLI must not block the detect-languages endpoint indefinitely.
const claudeDetectLangTimeout = 60 * time.Second

// claudeDetectLangAllowedIDs mirrors runtimeLanguageAllowlist's keys
// (devcontainer.go), repeated here (rather than importing it — it's
// unexported) so the prompt can list the exact allowed values without
// depending on internal agent package layout. Kept in id order for a stable,
// readable prompt.
var claudeDetectLangAllowedIDs = []string{"go", "java", "node", "python", "ruby", "rust"}

// ClaudeLanguageDetector is the agent.LLMDetector fallback used by
// POST /repos/{id}/detect-languages when the manifest scanner (D1) leaves a
// gap. It shells out to the claude CLI exactly once, in one-shot mode
// (--print --output-format json, not the streaming/PTY chat path used by
// ClaudeRunner/terminal.go — this is a single classification call, not an
// agent session) and returns raw, unvalidated guesses: DetectLanguagesWithFallback
// (detectlang_llm.go) is responsible for running them through
// agent.ParseRuntimeLanguages before they can become a suggestion. This type
// never persists anything and never touches the repo filesystem itself — it
// only sees the manifest contents/dir listing it's given.
type ClaudeLanguageDetector struct {
	// BinaryPath to the claude binary. Defaults to "claude" (resolved via
	// PATH), same convention as ClaudeRunner.BinaryPath.
	BinaryPath string
}

func (d *ClaudeLanguageDetector) binary() string {
	if d.BinaryPath != "" {
		return d.BinaryPath
	}
	return "claude"
}

// DetectLanguages implements agent.LLMDetector.
func (d *ClaudeLanguageDetector) DetectLanguages(ctx context.Context, input agent.LLMDetectInput) ([]agent.LanguageGuess, error) {
	prompt := buildDetectLangPrompt(input)

	runCtx, cancel := context.WithTimeout(ctx, claudeDetectLangTimeout)
	defer cancel()

	env := allowlistEnv(claudeEnvAllowlist)
	if tok := ClaudeOAuthAccessToken(); tok != "" {
		env = append(env, "ANTHROPIC_AUTH_TOKEN="+tok)
	}
	env = append(env, "DISABLE_AUTOUPDATER=1")

	args := sanitizeArgs([]string{
		"--print", prompt,
		"--output-format", "json",
		"--max-turns", "1",
	})
	// In-process only (empty runtimeSpec): language detection runs against
	// whatever repo checkout the backend itself can already read, never
	// inside a per-repo runtime container — there is no agent run in flight
	// yet to have provisioned one.
	cmd := spawn(runCtx, runtimeSpec{}, "", d.binary(), args, env)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("claude detect-languages: %w (%s)", err, strings.TrimSpace(stderr.String()))
	}

	return parseDetectLangOutput(stdout.Bytes())
}

// claudeJSONResult is the subset of `claude --output-format json`'s
// terminal object this call needs: the final assistant text lives in
// "result" for a one-shot --print invocation.
type claudeJSONResult struct {
	Result string `json:"result"`
}

// parseDetectLangOutput extracts the model's answer from the CLI's
// --output-format json envelope, then unmarshals that answer into the
// {id,version}[] shape the prompt asked for. Any parse failure at either
// layer is returned as an error, which DetectLanguagesWithFallback treats as
// "degrade to scan result" — never as a reason to guess or sanitize.
func parseDetectLangOutput(raw []byte) ([]agent.LanguageGuess, error) {
	var envelope claudeJSONResult
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, fmt.Errorf("parse claude json envelope: %w", err)
	}

	text := extractJSONArray(envelope.Result)
	if text == "" {
		return nil, fmt.Errorf("claude result contained no JSON array")
	}

	var guesses []agent.LanguageGuess
	if err := json.Unmarshal([]byte(text), &guesses); err != nil {
		return nil, fmt.Errorf("parse language guesses: %w", err)
	}
	return guesses, nil
}

// extractJSONArray returns the substring of s from its first '[' to its
// matching last ']', tolerating a model that wraps its JSON answer in prose
// or a markdown code fence despite being told not to. Returns "" if s
// contains no '[' or ']'.
func extractJSONArray(s string) string {
	start := strings.IndexByte(s, '[')
	end := strings.LastIndexByte(s, ']')
	if start < 0 || end < 0 || end < start {
		return ""
	}
	return s[start : end+1]
}

// buildDetectLangPrompt renders the manifest contents/dir listing into a
// prompt that constrains the model to a JSON array of {id,version} using
// only the allowlisted ids, and tells it to answer "[]" rather than guess
// when unsure — an empty answer beats a confident wrong one. Output is
// capped input, not capped by this function; callers (DetectLanguagesWithFallback)
// already bound ManifestContents/DirListing before this is called.
func buildDetectLangPrompt(input agent.LLMDetectInput) string {
	var b strings.Builder
	b.WriteString("You are identifying which programming language runtimes a software repository needs.\n\n")
	b.WriteString("Respond with ONLY a JSON array of objects, each shaped exactly {\"id\": string, \"version\": string}.\n")
	b.WriteString("The \"id\" field MUST be one of these exact values: ")
	b.WriteString(strings.Join(claudeDetectLangAllowedIDs, ", "))
	b.WriteString(".\n")
	b.WriteString("Do not invent an id outside that list. Do not include any other field, prose, or markdown formatting.\n")
	b.WriteString("If you cannot confidently determine a language and version from the material below, return an empty array: [].\n")
	b.WriteString("An empty or partial answer is better than a guess.\n\n")

	names := make([]string, 0, len(input.ManifestContents))
	for name := range input.ManifestContents {
		names = append(names, name)
	}
	sort.Strings(names)

	if len(names) > 0 {
		b.WriteString("Manifest files found in the repository:\n\n")
		for _, name := range names {
			b.WriteString("--- ")
			b.WriteString(name)
			b.WriteString(" ---\n")
			b.WriteString(input.ManifestContents[name])
			b.WriteString("\n\n")
		}
	}

	if len(input.DirListing) > 0 {
		b.WriteString("Shallow directory listing (repo root and one level of subdirectories):\n")
		for _, entry := range input.DirListing {
			b.WriteString("- ")
			b.WriteString(entry)
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}

	b.WriteString("Respond with only the JSON array.\n")
	return b.String()
}
