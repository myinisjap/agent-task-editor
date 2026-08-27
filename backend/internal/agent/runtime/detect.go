package runtime

import (
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// maxManifestReadBytes caps how much of any single manifest file Detect will
// read — these are tiny version-pin files by convention, so anything larger
// is treated as suspicious rather than parsed in full.
const maxManifestReadBytes = 64 * 1024

// Suggestion is one detected language/version pin, plus which manifest file
// it was read from (surfaced in the UI as a hint, e.g. "from go.mod").
type Suggestion struct {
	ID      string `json:"id"`
	Version string `json:"version"`
	Source  string `json:"source"`
}

// goModVersionPattern extracts the version after a `go` directive line, e.g.
// "go 1.21" or "go 1.21.3".
var goModVersionPattern = regexp.MustCompile(`(?m)^go\s+(\S+)`)

// rustToolchainChannelPattern extracts channel = "..." from a
// rust-toolchain.toml file.
var rustToolchainChannelPattern = regexp.MustCompile(`channel\s*=\s*"([^"]+)"`)

// manifestRule describes one manifest file Detect looks for: its filename
// (relative to the repo root), which language it maps to, and how to extract
// the version string from its contents.
type manifestRule struct {
	file    string
	lang    string
	extract func(contents string) (version string, ok bool)
}

func firstLineTrimmed(contents string) (string, bool) {
	line := strings.TrimSpace(strings.SplitN(contents, "\n", 2)[0])
	return line, line != ""
}

var manifestRules = []manifestRule{
	{file: "go.mod", lang: "go", extract: func(c string) (string, bool) {
		m := goModVersionPattern.FindStringSubmatch(c)
		if m == nil {
			return "", false
		}
		return m[1], true
	}},
	{file: ".nvmrc", lang: "node", extract: firstLineTrimmed},
	{file: ".node-version", lang: "node", extract: firstLineTrimmed},
	{file: ".python-version", lang: "python", extract: firstLineTrimmed},
	{file: "rust-toolchain", lang: "rust", extract: firstLineTrimmed},
	{file: "rust-toolchain.toml", lang: "rust", extract: func(c string) (string, bool) {
		m := rustToolchainChannelPattern.FindStringSubmatch(c)
		if m == nil {
			return "", false
		}
		return m[1], true
	}},
	{file: ".ruby-version", lang: "ruby", extract: firstLineTrimmed},
	{file: ".java-version", lang: "java", extract: firstLineTrimmed},
}

// Detect scans repoRoot (the repo's main clone path, never a task worktree)
// for well-known toolchain manifest files and returns a suggested pin for
// each one found. The root is scanned first, then each immediate
// subdirectory — one level deep only, no recursion — because monorepos keep
// their manifests in subdirectories (backend/go.mod, frontend/.nvmrc). A
// root finding wins per language; subdirectories are visited in os.ReadDir's
// sorted order so results are deterministic. This is a pure manifest scan —
// no LLM fallback, no filesystem writes. Every suggestion is validated the
// same way a human-submitted pin would be (ParsePins' allowlist + version
// regex); a manifest whose extracted version fails validation is silently
// dropped rather than surfaced as a bad suggestion. A symlinked manifest or
// directory is skipped (os.Lstat semantics) so Detect never follows a
// symlink planted in the repo out to an arbitrary filesystem path.
func Detect(repoRoot string) []Suggestion {
	var out []Suggestion
	// rust-toolchain vs rust-toolchain.toml both map to "rust" — only the
	// first match found wins, in manifestRules order, mirroring how mise
	// itself prefers the plain file.
	seenLang := map[string]bool{}

	scanManifestDir(repoRoot, "", seenLang, &out)

	entries, err := os.ReadDir(repoRoot)
	if err != nil {
		return out
	}
	for _, e := range entries {
		// IsDir() is false for symlinks (ReadDir lstats each entry), so a
		// symlinked directory is never followed.
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		// Dot-dirs (.git, .venv, …) and dependency trees aren't places a
		// repo declares its own toolchain.
		if strings.HasPrefix(name, ".") || name == "node_modules" || name == "vendor" {
			continue
		}
		scanManifestDir(filepath.Join(repoRoot, name), name+"/", seenLang, &out)
	}

	return out
}

// scanManifestDir applies manifestRules to one directory, appending a
// suggestion per newly seen language. sourcePrefix ("" for the repo root,
// "backend/" for a subdirectory) qualifies the Source shown in the UI.
func scanManifestDir(dir, sourcePrefix string, seenLang map[string]bool, out *[]Suggestion) {
	for _, rule := range manifestRules {
		if seenLang[rule.lang] {
			continue
		}
		path := filepath.Join(dir, rule.file)

		fi, err := os.Lstat(path)
		if err != nil {
			continue
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			continue // never follow a symlinked manifest
		}
		if !fi.Mode().IsRegular() {
			continue
		}

		f, err := os.Open(path)
		if err != nil {
			continue
		}
		data, err := io.ReadAll(io.LimitReader(f, maxManifestReadBytes))
		_ = f.Close()
		if err != nil {
			continue
		}

		version, ok := rule.extract(string(data))
		if !ok {
			continue
		}
		version = strings.TrimSpace(version)

		if !allowedLanguages[rule.lang] || !versionPattern.MatchString(version) {
			continue
		}

		*out = append(*out, Suggestion{ID: rule.lang, Version: version, Source: sourcePrefix + rule.file})
		seenLang[rule.lang] = true
	}
}
