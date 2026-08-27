package agent

import (
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// maxManifestReadBytes caps how much of any single manifest file this
// scanner reads into memory — a repo can commit an enormous file under one
// of these well-known names, and this is a scan, not a full parse of
// arbitrary user content. 256KB comfortably covers any real go.mod,
// package.json, pyproject.toml, etc.
const maxManifestReadBytes = 256 * 1024

// detectSkipDirs are never descended into during the scan — build output,
// vendored/third-party trees, and this codebase's own worktree scratch dir.
var detectSkipDirs = map[string]struct{}{
	".git":           {},
	"node_modules":   {},
	"vendor":         {},
	"target":         {},
	".ate-worktrees": {},
}

// LanguageSuggestion is one scanner (or, later, LLM) guess at a runtime
// language the repo uses. It is not validated here — DetectLanguages asserts
// every ID it returns is in runtimeLanguageAllowlist, but Version is only a
// best-effort extraction; the caller must still run the pair through
// ParseRuntimeLanguages before storing or trusting it (see the SECURITY
// comment in devcontainer.go — this scanner's output is as untrusted as any
// other repo content).
type LanguageSuggestion struct {
	ID        string // allowlisted runtime language id
	Version   string // "" when the language was found but no version could be read
	Source    string // the file the suggestion came from, e.g. "go.mod", "frontend/.nvmrc"
	Ambiguous bool   // a range or multiple candidates; Version (if set) is a best guess
}

// versionExtractor reads path (already size-capped) and returns a version
// string plus whether it's ambiguous (a range, not a pinned version).
// Returning ("", false, false) means "file didn't match" — try the next
// extractor for this language. Returning ok=true with an empty version means
// "the manifest is present but declares no version" — still a suggestion,
// per the plan's presence-without-version rule.
type versionExtractor struct {
	// relPath is the manifest filename to look for, relative to the
	// directory being scanned.
	relPath string
	// extract parses file content and returns (version, ambiguous, ok).
	// ok=false means this file didn't contain a usable signal at all (e.g.
	// wrong key present) and the scanner should still record bare presence
	// via the caller's fallback — see languageExtractors' doc.
	extract func(content string) (version string, ambiguous bool, ok bool)
}

// languageExtractors maps each allowlisted language id to its manifest
// files, in priority order (first match wins). This mirrors
// runtimeLanguageAllowlist's role as a fixed table: adding a language means
// adding one entry here, not a plugin system.
//
// Every extractor here returns ok=true merely for evidence-of-presence
// (e.g. an empty Cargo.toml) — a manifest existing on disk is itself the
// suggestion; extract() only refines it with a version when it can find one.
var languageExtractors = map[string][]versionExtractor{
	"go": {
		{relPath: "go.mod", extract: extractGoVersion},
	},
	"node": {
		{relPath: ".nvmrc", extract: extractPlainVersion},
		{relPath: "package.json", extract: extractNodeEngineVersion},
	},
	"python": {
		{relPath: ".python-version", extract: extractPlainVersion},
		{relPath: "pyproject.toml", extract: extractPyprojectPythonVersion},
		{relPath: "runtime.txt", extract: extractRuntimeTxtVersion},
	},
	"rust": {
		{relPath: "rust-toolchain.toml", extract: extractRustToolchainTomlVersion},
		{relPath: "rust-toolchain", extract: extractPlainVersion},
		{relPath: "Cargo.toml", extract: extractCargoTomlRustVersion},
	},
	"ruby": {
		{relPath: ".ruby-version", extract: extractPlainVersion},
		{relPath: "Gemfile", extract: extractGemfileRubyVersion},
	},
	"java": {
		{relPath: "pom.xml", extract: extractPomJavaVersion},
		{relPath: ".sdkmanrc", extract: extractSdkmanrcJavaVersion},
	},
}

// DetectLanguages scans a repo checkout for well-known manifest files and
// returns a best-effort suggestion per language found. It never writes
// anything and never errors on a per-file basis — a malformed manifest is
// skipped so the rest of the scan still completes (a repo with one broken
// package.json should still report its Go). The only error return is for a
// root-level problem reading the repo directory itself.
//
// Traversal is bounded: repo root plus one level of subdirectories (to catch
// monorepos, e.g. a frontend/ dir), skipping detectSkipDirs. This is a scan,
// not a full-tree walk.
func DetectLanguages(repoPath string) ([]LanguageSuggestion, error) {
	dirs, err := scanDirs(repoPath)
	if err != nil {
		return nil, fmt.Errorf("detect languages: %w", err)
	}

	// best[id] holds the current best suggestion for that language; a dir
	// earlier in `dirs` (repo root first) and an extractor earlier in its
	// priority list both win over a later match, per the plan's
	// "deduplicate by id, preferring the highest-priority source" rule.
	best := make(map[string]LanguageSuggestion)

	for _, dir := range dirs {
		for id, extractors := range languageExtractors {
			if _, done := best[id]; done {
				continue
			}
			for _, ve := range extractors {
				full := filepath.Join(dir, ve.relPath)
				content, ok := readManifestCapped(full)
				if !ok {
					continue
				}
				version, ambiguous, extractOK := ve.extract(content)
				if !extractOK {
					// Malformed manifest (e.g. invalid XML) — skip this
					// file, don't fail the whole scan, and don't record a
					// bogus presence for it either.
					continue
				}
				if version == "" {
					// Presence without a version still counts, but the user
					// must pick a version themselves — always ambiguous.
					ambiguous = true
				}
				source := ve.relPath
				if rel, relErr := filepath.Rel(repoPath, full); relErr == nil {
					source = rel
				}
				best[id] = LanguageSuggestion{
					ID:        id,
					Version:   version,
					Source:    source,
					Ambiguous: ambiguous,
				}
				break
			}
		}
	}

	suggestions := make([]LanguageSuggestion, 0, len(best))
	for id, s := range best {
		if _, allowed := runtimeLanguageAllowlist[id]; !allowed {
			// Extractors are a fixed table keyed by allowlisted ids, so this
			// can only happen if that table is edited incorrectly — a bug,
			// not a runtime condition. Fail loud rather than let an
			// unlisted id reach a caller that trusts this function's
			// contract.
			return nil, fmt.Errorf("detect languages: extractor produced non-allowlisted id %q", id)
		}
		suggestions = append(suggestions, s)
	}
	return suggestions, nil
}

// scanDirs returns repoPath plus its immediate subdirectories (excluding
// detectSkipDirs and hidden dirs), repo root first. A read error on repoPath
// itself is returned; a subdirectory that can't be listed is simply skipped.
func scanDirs(repoPath string) ([]string, error) {
	dirs := []string{repoPath}
	entries, err := os.ReadDir(repoPath)
	if err != nil {
		return nil, fmt.Errorf("read repo dir: %w", err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		if _, skip := detectSkipDirs[name]; skip {
			continue
		}
		if strings.HasPrefix(name, ".") {
			continue // no allowlisted manifest lives in a dotdir one level down
		}
		dirs = append(dirs, filepath.Join(repoPath, name))
	}
	return dirs, nil
}

// readManifestCapped reads up to maxManifestReadBytes of path. ok=false means
// "doesn't exist / can't be read / is a directory" — treated identically to
// a missing file by the caller, never a scan failure. ok=true with an empty
// string means the manifest exists but is empty, which still counts as
// presence (a touch'd Cargo.toml) — extractors handle empty content as "no
// version found" rather than "no match".
func readManifestCapped(path string) (string, bool) {
	// Refuse a manifest that is itself a symlink. os.Open follows links and
	// f.Stat() reports the target, so the IsDir guard below sees nothing
	// unusual: a repo committing `.nvmrc -> /etc/hostname` (or -> ~/.aws/
	// credentials) would have that file's contents read here and, on the
	// ambiguous path, packed into the prompt sent to the model by
	// buildLLMDetectInput. This runs in-process in the backend, which has
	// broad filesystem access and no container isolation — so this is a
	// read-side exfiltration path, distinct from the write-side "no user
	// string becomes a Docker flag" property devcontainer.go documents.
	// A manifest that is a symlink is rare enough in real repos that
	// refusing outright costs nothing.
	if li, err := os.Lstat(path); err != nil || li.Mode()&os.ModeSymlink != 0 {
		return "", false
	}

	f, err := os.Open(path)
	if err != nil {
		return "", false
	}
	defer func() { _ = f.Close() }()

	info, err := f.Stat()
	if err != nil || info.IsDir() {
		return "", false
	}

	buf := make([]byte, maxManifestReadBytes)
	n, err := f.Read(buf)
	if err != nil && n == 0 && !errors.Is(err, io.EOF) {
		return "", false
	}
	return string(buf[:n]), true
}

var goVersionRe = regexp.MustCompile(`(?m)^go\s+(\d+\.\d+(?:\.\d+)?)`)

func extractGoVersion(content string) (string, bool, bool) {
	m := goVersionRe.FindStringSubmatch(content)
	if m == nil {
		return "", false, true // go.mod present but no `go` directive found
	}
	return m[1], false, true
}

// normalizeVersion strips common range/prefix decorations (^, ~, >=, <=, >,
// <, =, leading "v") so a value that would otherwise be rejected by
// runtimeLanguageVersionRe (which forbids spaces and comparison characters)
// has a chance to survive ParseRuntimeLanguages. Returns the cleaned value
// and whether anything was stripped (a signal the original was a
// range/qualifier, so the caller should mark the suggestion Ambiguous).
func normalizeVersion(raw string) (string, bool) {
	v := strings.TrimSpace(raw)
	stripped := false
	for {
		trimmed := strings.TrimLeft(v, "^~=")
		trimmed = strings.TrimPrefix(trimmed, ">=")
		trimmed = strings.TrimPrefix(trimmed, "<=")
		trimmed = strings.TrimPrefix(trimmed, ">")
		trimmed = strings.TrimPrefix(trimmed, "<")
		trimmed = strings.TrimSpace(trimmed)
		if trimmed == v {
			break
		}
		v = trimmed
		stripped = true
	}
	// rbenv/chruby write ".ruby-version" as "ruby-3.3.0" about as often as a
	// bare "3.3.0"; the devcontainer ruby feature wants the bare version, and
	// "ruby-3.3.0" would pass ParseRuntimeLanguages (it's charset-legal) and
	// then fail at build time — the worst place to find out. Mirrors the
	// "python-" strip runtime.txt already does.
	if after, ok := strings.CutPrefix(v, "ruby-"); ok {
		v = after
		stripped = true
	}
	if strings.HasPrefix(v, "v") && len(v) > 1 && v[1] >= '0' && v[1] <= '9' {
		v = v[1:]
		stripped = true
	}
	// A range like ">=18 <21" has a space remaining after stripping the
	// leading qualifier — take just the first token as the best guess and
	// flag it ambiguous regardless.
	if idx := strings.IndexAny(v, " \t,|"); idx >= 0 {
		v = v[:idx]
		stripped = true
	}
	// Final gate: whatever survived must be something ParseRuntimeLanguages
	// will accept, or it isn't a usable suggestion. Values like
	// "lts/hydrogen" (.nvmrc), "${java.version}" (an unresolved pom.xml
	// property reference), and "*" (engines.node) are not ranges, so nothing
	// above strips or flags them — they'd reach the UI looking like confident
	// exact versions, render with no warning, and fail as an opaque 400 when
	// the user hits Save. Worse, a confident-looking scan suppresses the LLM
	// fallback that exists to answer exactly these cases.
	//
	// Degrade to presence-without-version instead: the UI already renders
	// that as "no version detected, pick one", and needsLLMFallback treats it
	// as a gap worth spending a call on. One guard here covers every current
	// extractor and any added later, which three per-extractor patches would
	// not.
	if v != "" && (len(v) > runtimeLanguageVersionMaxLen || !runtimeLanguageVersionRe.MatchString(v)) {
		return "", true
	}
	return v, stripped
}

func extractPlainVersion(content string) (string, bool, bool) {
	line := strings.TrimSpace(strings.SplitN(content, "\n", 2)[0])
	if line == "" {
		return "", false, true // present but empty
	}
	v, ambiguous := normalizeVersion(line)
	return v, ambiguous, true
}

var nodeEnginesRe = regexp.MustCompile(`"engines"\s*:\s*\{[^}]*"node"\s*:\s*"([^"]+)"`)

func extractNodeEngineVersion(content string) (string, bool, bool) {
	m := nodeEnginesRe.FindStringSubmatch(content)
	if m == nil {
		return "", false, true // package.json present but no engines.node
	}
	raw := strings.TrimSpace(m[1])
	// Any range syntax at all (space-separated bounds, ||, or a leading
	// comparison operator) is ambiguous even after normalization picks a
	// single best-guess token from it.
	isRange := strings.ContainsAny(raw, " |") || strings.HasPrefix(raw, ">") || strings.HasPrefix(raw, "<") || strings.HasPrefix(raw, "^") || strings.HasPrefix(raw, "~")
	v, stripped := normalizeVersion(raw)
	return v, isRange || stripped, true
}

var pyprojectPythonRe = regexp.MustCompile(`(?m)^\s*requires-python\s*=\s*"([^"]+)"`)

func extractPyprojectPythonVersion(content string) (string, bool, bool) {
	m := pyprojectPythonRe.FindStringSubmatch(content)
	if m == nil {
		return "", false, true // pyproject.toml present but no requires-python
	}
	raw := strings.TrimSpace(m[1])
	isRange := strings.ContainsAny(raw, " ,|") || strings.HasPrefix(raw, ">") || strings.HasPrefix(raw, "<") || strings.HasPrefix(raw, "^") || strings.HasPrefix(raw, "~") || strings.HasPrefix(raw, "=")
	v, stripped := normalizeVersion(raw)
	return v, isRange || stripped, true
}

func extractRuntimeTxtVersion(content string) (string, bool, bool) {
	// Heroku-style runtime.txt: "python-3.12.1"
	line := strings.TrimSpace(strings.SplitN(content, "\n", 2)[0])
	if line == "" {
		return "", false, true
	}
	v := strings.TrimPrefix(line, "python-")
	v, ambiguous := normalizeVersion(v)
	return v, ambiguous, true
}

var rustToolchainChannelRe = regexp.MustCompile(`(?m)^\s*channel\s*=\s*"([^"]+)"`)

func extractRustToolchainTomlVersion(content string) (string, bool, bool) {
	m := rustToolchainChannelRe.FindStringSubmatch(content)
	if m == nil {
		return "", false, true
	}
	raw := strings.TrimSpace(m[1])
	// "stable"/"beta"/"nightly" aren't versions runtimeLanguageVersionRe
	// would reject, but they're not a version either — treat as ambiguous
	// presence rather than a confident pinned version.
	if raw == "stable" || raw == "beta" || raw == "nightly" {
		return "", true, true
	}
	v, ambiguous := normalizeVersion(raw)
	return v, ambiguous, true
}

var cargoRustVersionRe = regexp.MustCompile(`(?m)^\s*rust-version\s*=\s*"([^"]+)"`)

func extractCargoTomlRustVersion(content string) (string, bool, bool) {
	m := cargoRustVersionRe.FindStringSubmatch(content)
	if m == nil {
		return "", false, true // Cargo.toml present but no rust-version — presence still counts
	}
	v, ambiguous := normalizeVersion(strings.TrimSpace(m[1]))
	return v, ambiguous, true
}

var gemfileRubyRe = regexp.MustCompile(`(?m)^\s*ruby\s+['"]([^'"]+)['"]`)

func extractGemfileRubyVersion(content string) (string, bool, bool) {
	m := gemfileRubyRe.FindStringSubmatch(content)
	if m == nil {
		return "", false, true
	}
	v, ambiguous := normalizeVersion(strings.TrimSpace(m[1]))
	return v, ambiguous, true
}

// pomProject is the minimal subset of a Maven pom.xml this extractor reads.
type pomProject struct {
	Properties struct {
		MavenCompilerRelease string `xml:"maven.compiler.release"`
		JavaVersion          string `xml:"java.version"`
	} `xml:"properties"`
}

func extractPomJavaVersion(content string) (string, bool, bool) {
	var p pomProject
	if err := xml.Unmarshal([]byte(content), &p); err != nil {
		return "", false, false // malformed XML — skip, don't fail the scan
	}
	raw := strings.TrimSpace(p.Properties.MavenCompilerRelease)
	if raw == "" {
		raw = strings.TrimSpace(p.Properties.JavaVersion)
	}
	if raw == "" {
		return "", false, true // pom.xml present but no version property found
	}
	v, ambiguous := normalizeVersion(raw)
	return v, ambiguous, true
}

var sdkmanrcJavaRe = regexp.MustCompile(`(?m)^\s*java\s*=\s*(\S+)`)

func extractSdkmanrcJavaVersion(content string) (string, bool, bool) {
	m := sdkmanrcJavaRe.FindStringSubmatch(content)
	if m == nil {
		return "", false, true
	}
	raw := strings.TrimSpace(m[1])
	// sdkman identifiers look like "17.0.2-tem" — take the leading numeric
	// portion as the best-guess version and flag anything else ambiguous.
	v, ambiguous := normalizeVersion(raw)
	if idx := strings.IndexByte(v, '-'); idx >= 0 {
		v = v[:idx]
		ambiguous = true
	}
	return v, ambiguous, true
}
