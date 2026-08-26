package agent

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
)

// llmDetectMaxFiles/llmDetectMaxDirEntries/llmDetectMaxTotalBytes bound what
// DetectLanguagesWithFallback sends to the LLM: manifest file contents plus a
// shallow directory listing, never the whole repo. See the SECURITY note on
// LanguageGuess below for why the *output* side needs just as hard a bound.
const (
	llmDetectMaxDirEntries = 200
	llmDetectMaxTotalBytes = 64 * 1024
)

// LanguageGuess is one {id, version} pair as returned by an LLMDetector. It
// is untrusted model output, not a validated suggestion — the only thing
// that may ever come from an LLMDetector.DetectLanguages call is this loose
// shape; DetectLanguagesWithFallback runs every guess through
// ParseRuntimeLanguages before it can become a LanguageSuggestion, exactly
// like any other untrusted string source (see the SECURITY comment in
// devcontainer.go). A guess that fails validation is dropped and logged,
// never sanitized or passed through.
type LanguageGuess struct {
	ID      string
	Version string
}

// LLMDetectInput is what DetectLanguagesWithFallback feeds an LLMDetector:
// the contents of the manifest files the scanner already found (capped, not
// the whole repo) plus a shallow directory listing for extra context.
type LLMDetectInput struct {
	// ManifestContents maps a repo-relative path (e.g. "go.mod",
	// "frontend/package.json") to its (already size-capped) content.
	ManifestContents map[string]string
	// DirListing is a shallow (repo root + one level) list of relative paths,
	// capped at llmDetectMaxDirEntries.
	DirListing []string
}

// LLMDetector is the fallback path invoked only when the manifest scan
// (DetectLanguages) leaves a gap. Implementations must be injectable so
// tests never need a real CLI — see providers.ClaudeLanguageDetector for the
// production implementation (a one-shot `claude --print` call).
type LLMDetector interface {
	DetectLanguages(ctx context.Context, input LLMDetectInput) ([]LanguageGuess, error)
}

// needsLLMFallback reports whether scan results leave a gap the plan says is
// worth spending an LLM call on: nothing found, or any suggestion flagged
// Ambiguous. A complete, unambiguous scan must never reach the LLM — that
// would spend money and latency to produce a worse answer than the manifest
// already gave.
func needsLLMFallback(suggestions []LanguageSuggestion) bool {
	if len(suggestions) == 0 {
		return true
	}
	for _, s := range suggestions {
		if s.Ambiguous {
			return true
		}
	}
	return false
}

// DetectLanguagesWithFallback runs the manifest scanner and, only if it
// leaves a gap (see needsLLMFallback), falls back to llm. It never persists
// anything and never errors on an LLM failure: a nil llm, a call error, a
// timeout, or output that fails validation all degrade to the scan's own
// result with usedLLM=false — a suggestion endpoint returning a worse
// (scan-only) answer beats one that 500s. The scanner's own root-level error
// (unreadable repoPath) is still returned, since there is nothing to fall
// back to in that case.
func DetectLanguagesWithFallback(ctx context.Context, repoPath string, llm LLMDetector) ([]LanguageSuggestion, bool, error) {
	scanned, err := DetectLanguages(repoPath)
	if err != nil {
		return nil, false, err
	}
	if !needsLLMFallback(scanned) {
		return scanned, false, nil
	}
	if llm == nil {
		return scanned, false, nil
	}

	input := buildLLMDetectInput(repoPath, scanned)
	guesses, err := llm.DetectLanguages(ctx, input)
	if err != nil {
		slog.Warn("detect languages: llm fallback failed, using scan result", "repo_path", repoPath, "err", err)
		return scanned, false, nil
	}

	validated := validateLLMGuesses(guesses)
	if len(validated) == 0 {
		// Every guess was dropped (empty/garbage output, or a deliberate "[]"
		// when the model was unsure) — an empty answer beats a confident
		// wrong one, but it's still no better than the scan we already have.
		return scanned, false, nil
	}
	return mergeSuggestions(scanned, validated), true, nil
}

// buildLLMDetectInput gathers the manifest contents the scan already read
// (by re-reading each suggestion's Source file, capped) plus a shallow
// directory listing, bounded by llmDetectMaxTotalBytes/llmDetectMaxDirEntries
// so the LLM call never receives the whole repo.
func buildLLMDetectInput(repoPath string, scanned []LanguageSuggestion) LLMDetectInput {
	input := LLMDetectInput{ManifestContents: make(map[string]string, len(scanned))}

	remaining := llmDetectMaxTotalBytes
	for _, s := range scanned {
		if s.Source == "" || remaining <= 0 {
			continue
		}
		content, ok := readManifestCapped(filepath.Join(repoPath, s.Source))
		if !ok {
			continue
		}
		if len(content) > remaining {
			content = content[:remaining]
		}
		input.ManifestContents[s.Source] = content
		remaining -= len(content)
	}

	dirs, err := scanDirs(repoPath)
	if err == nil {
		for _, dir := range dirs {
			entries, rerr := os.ReadDir(dir)
			if rerr != nil {
				continue
			}
			for _, e := range entries {
				if len(input.DirListing) >= llmDetectMaxDirEntries {
					break
				}
				rel, rerr := filepath.Rel(repoPath, filepath.Join(dir, e.Name()))
				if rerr != nil {
					continue
				}
				input.DirListing = append(input.DirListing, rel)
			}
		}
	}
	return input
}

// validateLLMGuesses runs every LLM-produced guess through
// ParseRuntimeLanguages, the same validation any other untrusted
// runtime_languages source must pass. An unknown id, a version with
// disallowed characters, or a duplicate id causes ParseRuntimeLanguages to
// reject the whole batch — since a single hostile/malformed entry taints the
// batch's trustworthiness, this drops all of it rather than picking through
// for the "good" entries, and the caller degrades to the scan result.
func validateLLMGuesses(guesses []LanguageGuess) []RuntimeLanguage {
	if len(guesses) == 0 {
		return nil
	}
	langs := make([]RuntimeLanguage, 0, len(guesses))
	for _, g := range guesses {
		langs = append(langs, RuntimeLanguage(g))
	}
	data, err := json.Marshal(langs)
	if err != nil {
		slog.Warn("detect languages: failed to marshal llm guesses for validation", "err", err)
		return nil
	}
	validated, err := ParseRuntimeLanguages(string(data))
	if err != nil {
		slog.Warn("detect languages: llm output failed validation, dropping", "err", err)
		return nil
	}
	return validated
}

// mergeSuggestions overlays LLM-validated guesses onto the scan result: an id
// the scan already found (even ambiguously) keeps its scan-derived Source so
// the UI can still show where the value came from, while a language the scan
// found no evidence of at all is added as an LLM-only suggestion.
func mergeSuggestions(scanned []LanguageSuggestion, llmLangs []RuntimeLanguage) []LanguageSuggestion {
	byID := make(map[string]LanguageSuggestion, len(scanned))
	for _, s := range scanned {
		byID[s.ID] = s
	}
	for _, l := range llmLangs {
		existing, ok := byID[l.ID]
		if ok && !existing.Ambiguous && existing.Version != "" {
			// Scan already had a confident answer for this id — an LLM guess
			// for the same id never overrides it (only truly-ambiguous or
			// missing scan entries reach here in practice, but this keeps the
			// merge safe regardless of what a future LLMDetector returns).
			continue
		}
		byID[l.ID] = LanguageSuggestion{
			ID:        l.ID,
			Version:   l.Version,
			Source:    "claude",
			Ambiguous: false,
		}
	}

	out := make([]LanguageSuggestion, 0, len(byID))
	for _, s := range byID {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
