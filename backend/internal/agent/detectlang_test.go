package agent

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// writeFile is a small test helper: write content to dir/relPath, creating
// parent directories as needed.
func writeFile(t *testing.T, dir, relPath, content string) {
	t.Helper()
	full := filepath.Join(dir, relPath)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir for %s: %v", relPath, err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", relPath, err)
	}
}

// byID indexes suggestions by language id for assertions.
func byID(suggestions []LanguageSuggestion) map[string]LanguageSuggestion {
	m := make(map[string]LanguageSuggestion, len(suggestions))
	for _, s := range suggestions {
		m[s.ID] = s
	}
	return m
}

func TestDetectLanguages_EmptyRepo(t *testing.T) {
	dir := t.TempDir()
	suggestions, err := DetectLanguages(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(suggestions) != 0 {
		t.Fatalf("expected no suggestions, got %+v", suggestions)
	}
}

func TestDetectLanguages_Go(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module example.com/foo\n\ngo 1.26\n")

	suggestions, err := DetectLanguages(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := byID(suggestions)
	s, ok := got["go"]
	if !ok {
		t.Fatalf("expected a go suggestion, got %+v", suggestions)
	}
	if s.Version != "1.26" || s.Ambiguous || s.Source != "go.mod" {
		t.Errorf("unexpected go suggestion: %+v", s)
	}
}

func TestDetectLanguages_NodeExactViaNvmrc(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, ".nvmrc", "18.19.0\n")

	suggestions, err := DetectLanguages(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	s, ok := byID(suggestions)["node"]
	if !ok {
		t.Fatalf("expected a node suggestion, got %+v", suggestions)
	}
	if s.Version != "18.19.0" || s.Ambiguous || s.Source != ".nvmrc" {
		t.Errorf("unexpected node suggestion: %+v", s)
	}
}

func TestDetectLanguages_Python(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, ".python-version", "3.12.1\n")

	suggestions, err := DetectLanguages(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	s, ok := byID(suggestions)["python"]
	if !ok {
		t.Fatalf("expected a python suggestion, got %+v", suggestions)
	}
	if s.Version != "3.12.1" || s.Ambiguous {
		t.Errorf("unexpected python suggestion: %+v", s)
	}
}

func TestDetectLanguages_Ruby(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, ".ruby-version", "3.3.0\n")

	suggestions, err := DetectLanguages(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	s, ok := byID(suggestions)["ruby"]
	if !ok {
		t.Fatalf("expected a ruby suggestion, got %+v", suggestions)
	}
	if s.Version != "3.3.0" || s.Ambiguous {
		t.Errorf("unexpected ruby suggestion: %+v", s)
	}
}

func TestDetectLanguages_Java(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "pom.xml", `<project>
  <properties>
    <maven.compiler.release>21</maven.compiler.release>
  </properties>
</project>
`)

	suggestions, err := DetectLanguages(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	s, ok := byID(suggestions)["java"]
	if !ok {
		t.Fatalf("expected a java suggestion, got %+v", suggestions)
	}
	if s.Version != "21" || s.Ambiguous || s.Source != "pom.xml" {
		t.Errorf("unexpected java suggestion: %+v", s)
	}
}

func TestDetectLanguages_RustExactViaToolchainToml(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "rust-toolchain.toml", "[toolchain]\nchannel = \"1.75.0\"\n")

	suggestions, err := DetectLanguages(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	s, ok := byID(suggestions)["rust"]
	if !ok {
		t.Fatalf("expected a rust suggestion, got %+v", suggestions)
	}
	if s.Version != "1.75.0" || s.Ambiguous || s.Source != "rust-toolchain.toml" {
		t.Errorf("unexpected rust suggestion: %+v", s)
	}
}

// TestDetectLanguages_RustPresenceWithoutVersion covers the plan's
// non-negotiable rule: a Cargo.toml with no rust-version still yields a
// suggestion (Rust is here), just with no version and Ambiguous=true so the
// user knows to pick one.
func TestDetectLanguages_RustPresenceWithoutVersion(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "Cargo.toml", "[package]\nname = \"foo\"\nedition = \"2021\"\n")

	suggestions, err := DetectLanguages(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	s, ok := byID(suggestions)["rust"]
	if !ok {
		t.Fatalf("expected a rust suggestion for bare presence, got %+v", suggestions)
	}
	if s.Version != "" {
		t.Errorf("expected empty version, got %q", s.Version)
	}
	if !s.Ambiguous {
		t.Errorf("expected Ambiguous=true for presence-without-version")
	}
	if s.Source != "Cargo.toml" {
		t.Errorf("unexpected source: %q", s.Source)
	}
}

// TestDetectLanguages_NodeRangeIsAmbiguous covers the plan's range rule: an
// engines.node range extracts a best-guess version but is flagged Ambiguous.
func TestDetectLanguages_NodeRangeIsAmbiguous(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "package.json", `{"name":"foo","engines":{"node":">=18 <21"}}`)

	suggestions, err := DetectLanguages(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	s, ok := byID(suggestions)["node"]
	if !ok {
		t.Fatalf("expected a node suggestion, got %+v", suggestions)
	}
	if !s.Ambiguous {
		t.Errorf("expected Ambiguous=true for a range, got %+v", s)
	}
	if s.Version != "18" {
		t.Errorf("expected best-guess version 18, got %q", s.Version)
	}
}

// TestDetectLanguages_Monorepo covers Go at root + Node in a subdirectory —
// the one-level-of-subdirectories traversal rule.
func TestDetectLanguages_Monorepo(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module example.com/foo\n\ngo 1.25\n")
	writeFile(t, dir, "frontend/.nvmrc", "20\n")

	suggestions, err := DetectLanguages(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := byID(suggestions)
	if len(got) != 2 {
		t.Fatalf("expected 2 suggestions, got %+v", suggestions)
	}
	if got["go"].Version != "1.25" {
		t.Errorf("unexpected go suggestion: %+v", got["go"])
	}
	if got["node"].Version != "20" || got["node"].Source != "frontend/.nvmrc" {
		t.Errorf("unexpected node suggestion: %+v", got["node"])
	}
}

// TestDetectLanguages_MalformedManifestDoesNotFailScan covers the plan's
// non-negotiable: one broken manifest must not sink suggestions for the rest
// of the repo.
func TestDetectLanguages_MalformedManifestDoesNotFailScan(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module example.com/foo\n\ngo 1.26\n")
	writeFile(t, dir, "pom.xml", "<project><properties>not valid xml")

	suggestions, err := DetectLanguages(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := byID(suggestions)
	if _, ok := got["go"]; !ok {
		t.Fatalf("expected go to still be detected despite malformed pom.xml, got %+v", suggestions)
	}
	if _, ok := got["java"]; ok {
		t.Errorf("expected no java suggestion from malformed pom.xml, got %+v", got["java"])
	}
}

// TestDetectLanguages_SkipsIgnoredDirs ensures node_modules/vendor/etc. one
// level down are never scanned — a stray go.mod or package.json living
// inside a vendored dependency must not surface as a suggestion.
func TestDetectLanguages_SkipsIgnoredDirs(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "node_modules/some-pkg/package.json", `{"engines":{"node":"16"}}`)
	writeFile(t, dir, "vendor/some-dep/go.mod", "module vendored\n\ngo 1.20\n")

	suggestions, err := DetectLanguages(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(suggestions) != 0 {
		t.Fatalf("expected no suggestions from skipped dirs, got %+v", suggestions)
	}
}

// TestDetectLanguages_PriorityOrder covers "deduplicate by id, preferring
// the highest-priority source": .nvmrc must win over package.json engines.
func TestDetectLanguages_PriorityOrder(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, ".nvmrc", "18\n")
	writeFile(t, dir, "package.json", `{"engines":{"node":"20"}}`)

	suggestions, err := DetectLanguages(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	s, ok := byID(suggestions)["node"]
	if !ok {
		t.Fatalf("expected a node suggestion, got %+v", suggestions)
	}
	if s.Version != "18" || s.Source != ".nvmrc" {
		t.Errorf("expected .nvmrc (higher priority) to win, got %+v", s)
	}
}

// TestDetectLanguages_AllSuggestionsSurviveParseRuntimeLanguages is the
// round-trip assertion the plan calls for: every suggestion this scanner
// produces, once given a non-empty version, must pass ParseRuntimeLanguages
// — a version this scanner extracts must never be one ParseRuntimeLanguages
// would reject. Suggestions with an empty Version (bare presence) are
// exempt, since the UI is expected to require the user to pick a version for
// those before save — ParseRuntimeLanguages itself rejects empty versions by
// contract.
func TestDetectLanguages_AllSuggestionsSurviveParseRuntimeLanguages(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module example.com/foo\n\ngo 1.26\n")
	writeFile(t, dir, "Cargo.toml", "[package]\nname = \"foo\"\n") // presence only
	writeFile(t, dir, "pom.xml", `<project><properties><java.version>17</java.version></properties></project>`)
	writeFile(t, dir, "Gemfile", "source 'https://rubygems.org'\nruby '3.2.1'\n")
	writeFile(t, dir, "frontend/.nvmrc", "^18.0.0\n")
	writeFile(t, dir, "backend2/.python-version", ">=3.10,<3.13\n")

	suggestions, err := DetectLanguages(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(suggestions) == 0 {
		t.Fatal("expected at least one suggestion")
	}

	for _, s := range suggestions {
		if s.Version == "" {
			continue // bare presence; ParseRuntimeLanguages requires a version, by design the UI fills it in
		}
		raw := `[{"id":"` + s.ID + `","version":"` + s.Version + `"}]`
		if _, err := ParseRuntimeLanguages(raw); err != nil {
			t.Errorf("suggestion %+v produced a version that fails ParseRuntimeLanguages: %v", s, err)
		}
	}
}

// TestDetectLanguages_EveryReturnedIDIsAllowlisted is a cheap sanity check
// that the extractor table can never emit an id outside
// runtimeLanguageAllowlist — the plan calls this out as a bug, not a runtime
// condition, so DetectLanguages already asserts it internally; this test
// exercises that path across every configured extractor id.
func TestDetectLanguages_EveryReturnedIDIsAllowlisted(t *testing.T) {
	ids := make([]string, 0, len(languageExtractors))
	for id := range languageExtractors {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		if _, ok := runtimeLanguageAllowlist[id]; !ok {
			t.Errorf("languageExtractors has id %q not present in runtimeLanguageAllowlist", id)
		}
	}
}

func TestDetectLanguages_NoSuchRepoPath(t *testing.T) {
	if _, err := DetectLanguages(filepath.Join(t.TempDir(), "does-not-exist")); err == nil {
		t.Fatal("expected an error for a nonexistent repo path")
	}
}
