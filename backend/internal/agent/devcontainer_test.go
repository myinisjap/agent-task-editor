package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --- ParseRuntimeLanguages (allowlist + version validation) ---

func TestParseRuntimeLanguages_Empty(t *testing.T) {
	langs, err := ParseRuntimeLanguages("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if langs != nil {
		t.Errorf("expected nil for empty input, got %v", langs)
	}
}

func TestParseRuntimeLanguages_Valid(t *testing.T) {
	langs, err := ParseRuntimeLanguages(`[{"id":"go","version":"1.26"},{"id":"python","version":"3.12"}]`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(langs) != 2 {
		t.Fatalf("expected 2 languages, got %d", len(langs))
	}
	if langs[0].ID != "go" || langs[0].Version != "1.26" {
		t.Errorf("unexpected first entry: %+v", langs[0])
	}
	if langs[1].ID != "python" || langs[1].Version != "3.12" {
		t.Errorf("unexpected second entry: %+v", langs[1])
	}
}

func TestParseRuntimeLanguages_MalformedJSON(t *testing.T) {
	if _, err := ParseRuntimeLanguages("{not valid json"); err == nil {
		t.Fatal("expected an error for malformed JSON")
	}
}

// TestParseRuntimeLanguages_RejectsDuplicateIDs guards a silent-data-loss
// path: GenerateDevcontainerJSON keys features by feature ref, so two entries
// with the same id collapse to whichever came last. Before this rejection,
// picking Go 1.26 and Go 1.21 saved with a 200, redisplayed both rows in the
// UI, and built a container with only 1.21 — the user's build failing against
// a version the UI said was configured, with no error anywhere.
func TestParseRuntimeLanguages_RejectsDuplicateIDs(t *testing.T) {
	langs, err := ParseRuntimeLanguages(`[{"id":"go","version":"1.26"},{"id":"go","version":"1.21"}]`)
	if err == nil {
		t.Fatalf("duplicate id accepted, got %d langs; want an error", len(langs))
	}
	if !strings.Contains(err.Error(), "duplicate") {
		t.Errorf("error %q should name the problem as a duplicate", err)
	}
	// Distinct ids must still be accepted — the check is per-id, not a
	// blanket ban on multiple languages.
	if _, err := ParseRuntimeLanguages(`[{"id":"go","version":"1.26"},{"id":"python","version":"3.12"}]`); err != nil {
		t.Errorf("distinct ids rejected: %v", err)
	}
}

// TestParseRuntimeLanguages_RejectsInjectionShapedInputs is the core security
// test: an id or version crafted to look like a Docker flag or shell command
// must be rejected outright (400 at the handler layer), never silently
// sanitized or passed through. This is what makes "no user string reaches
// runArgs/mounts" true — the rejection happens here, before
// GenerateDevcontainerJSON ever runs.
func TestParseRuntimeLanguages_RejectsInjectionShapedInputs(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		{"id is a docker flag", `[{"id":"--privileged","version":"1"}]`},
		{"id is a shell injection attempt", `[{"id":"; rm -rf /","version":"1"}]`},
		{"version is a shell injection attempt", `[{"id":"go","version":"; rm -rf /"}]`},
		{"version contains spaces", `[{"id":"go","version":"1.26 --privileged"}]`},
		{"version contains double quotes", `[{"id":"go","version":"1.26\""}]`},
		{"version contains single quotes", `[{"id":"go","version":"1.26'"}]`},
		{"version contains a comma (mount-field breakout attempt)", `[{"id":"go","version":"1,type=bind"}]`},
		{"version starts with a dash", `[{"id":"go","version":"-privileged"}]`},
		{"version is empty", `[{"id":"go","version":""}]`},
		{"version exceeds max length", `[{"id":"go","version":"` + strings.Repeat("a", 33) + `"}]`},
		{"unknown id", `[{"id":"cobol","version":"1"}]`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ParseRuntimeLanguages(tc.raw); err == nil {
				t.Fatalf("expected an error for %s, got none", tc.raw)
			}
		})
	}
}

func TestParseRuntimeLanguages_MaxLengthVersionAccepted(t *testing.T) {
	raw := `[{"id":"go","version":"` + strings.Repeat("a", 32) + `"}]`
	if _, err := ParseRuntimeLanguages(raw); err != nil {
		t.Fatalf("expected a 32-char version to be accepted, got error: %v", err)
	}
}

// --- ReadRepoDevcontainerFile ---

func TestReadRepoDevcontainerFile_Absent(t *testing.T) {
	dir := t.TempDir()
	got, err := ReadRepoDevcontainerFile(dir)
	if err != nil {
		t.Fatalf("expected no error for a missing devcontainer.json, got %v", err)
	}
	if got != "" {
		t.Errorf("expected empty string for a missing file, got %q", got)
	}
}

func TestReadRepoDevcontainerFile_Present(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".devcontainer"), 0o755); err != nil {
		t.Fatal(err)
	}
	want := `{"image":"golang:1.26"}`
	if err := os.WriteFile(filepath.Join(dir, devcontainerFilePath), []byte(want), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := ReadRepoDevcontainerFile(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// --- GenerateDevcontainerJSON (generation, not merge) ---

// TestGenerateDevcontainerJSON_FullMountContract verifies the generated JSON
// contains the complete mount contract (same-path repo, /tmp, sidecar ro,
// all credential dirs) and the hardening runArgs, plus a features entry per
// requested language.
func TestGenerateDevcontainerJSON_FullMountContract(t *testing.T) {
	home := t.TempDir()
	// Credential mounts are emitted only for dirs that exist (docker run
	// rejects a bind with a missing source), so create them to assert the
	// full contract here.
	for _, d := range credentialDirs {
		if err := os.MkdirAll(filepath.Join(home, d), 0o700); err != nil {
			t.Fatal(err)
		}
	}

	out, err := GenerateDevcontainerJSON(
		[]RuntimeLanguage{{ID: "go", Version: "1.26"}, {ID: "python", Version: "3.12"}},
		injectedContract{
			RepoPath:          "/data/repos/myrepo",
			MCPServerPath:     "/opt/ate/mcp-server",
			HostMCPBindSource: "/opt/ate/mcp-server",
			HostHome:          home,
		},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var cfg map[string]any
	if err := json.Unmarshal([]byte(out), &cfg); err != nil {
		t.Fatalf("generated JSON did not parse: %v", err)
	}

	if got := cfg["workspaceMount"]; got != "source=/data/repos/myrepo,target=/data/repos/myrepo,type=bind" {
		t.Errorf("workspaceMount = %v", got)
	}
	if got := cfg["workspaceFolder"]; got != "/data/repos/myrepo" {
		t.Errorf("workspaceFolder = %v", got)
	}
	if got := cfg["image"]; got != devcontainerBaseImage {
		t.Errorf("image = %v, want %v", got, devcontainerBaseImage)
	}

	features, ok := cfg["features"].(map[string]any)
	if !ok {
		t.Fatalf("expected features to be an object, got %T", cfg["features"])
	}
	if _, ok := features["ghcr.io/devcontainers/features/go:1"]; !ok {
		t.Errorf("expected go feature ref in %v", features)
	}
	if _, ok := features["ghcr.io/devcontainers/features/python:1"]; !ok {
		t.Errorf("expected python feature ref in %v", features)
	}

	mounts := stringSliceField(cfg["mounts"])
	if !containsSubstring(mounts, "source=/tmp,target=/tmp,type=bind") {
		t.Errorf("expected /tmp mount in %v", mounts)
	}
	if !containsSubstring(mounts, "source=/opt/ate/mcp-server,target=/opt/ate/mcp-server,type=bind,readonly") {
		t.Errorf("expected read-only MCP sidecar mount in %v", mounts)
	}
	for _, dir := range credentialDirs {
		want := "source=" + filepath.Join(home, dir) + ",target=" + RuntimeContainerHome + "/" + dir + ",type=bind"
		if !containsSubstring(mounts, want) {
			t.Errorf("expected credential mount %q in %v", want, mounts)
		}
	}

	runArgs := stringSliceField(cfg["runArgs"])
	if !containsSubstring(runArgs, "--security-opt=no-new-privileges") {
		t.Errorf("expected no-new-privileges in runArgs %v", runArgs)
	}
	if !containsSubstring(runArgs, "--cap-drop=ALL") {
		t.Errorf("expected cap-drop=ALL in runArgs %v", runArgs)
	}
}

// TestGenerateDevcontainerJSON_NoUserStringReachesRunArgsOrMounts is the
// direct security-property test: even with attacker-shaped (but
// allowlist-valid) input, runArgs and mounts contain only the fixed
// hardening/contract strings — never anything derived from langs beyond the
// validated version placed inside a features value, which this asserts is
// NOT present in runArgs/mounts at all.
func TestGenerateDevcontainerJSON_NoUserStringReachesRunArgsOrMounts(t *testing.T) {
	const sentinelVersion = "1.99.99" // stands in for "attacker-controlled but allowlist-valid"
	out, err := GenerateDevcontainerJSON(
		[]RuntimeLanguage{{ID: "go", Version: sentinelVersion}},
		injectedContract{RepoPath: "/repo"},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var cfg map[string]any
	if err := json.Unmarshal([]byte(out), &cfg); err != nil {
		t.Fatal(err)
	}

	runArgs := stringSliceField(cfg["runArgs"])
	if len(runArgs) != 2 {
		t.Fatalf("expected exactly the 2 hardening runArgs, got %v", runArgs)
	}
	for _, a := range runArgs {
		if strings.Contains(a, sentinelVersion) {
			t.Errorf("version string leaked into runArgs: %v", runArgs)
		}
	}
	mounts := stringSliceField(cfg["mounts"])
	for _, m := range mounts {
		if strings.Contains(m, sentinelVersion) {
			t.Errorf("version string leaked into mounts: %v", mounts)
		}
	}
}

func TestGenerateDevcontainerJSON_UnknownIDErrors(t *testing.T) {
	// Defense in depth: GenerateDevcontainerJSON itself also rejects an
	// unknown id, even though ParseRuntimeLanguages is the intended gate.
	_, err := GenerateDevcontainerJSON([]RuntimeLanguage{{ID: "cobol", Version: "1"}}, injectedContract{RepoPath: "/repo"})
	if err == nil {
		t.Fatal("expected an error for an unknown language id")
	}
}

func TestGenerateDevcontainerJSON_EmptyLanguagesStillInjectsContract(t *testing.T) {
	out, err := GenerateDevcontainerJSON(nil, injectedContract{RepoPath: "/repo"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var cfg map[string]any
	if err := json.Unmarshal([]byte(out), &cfg); err != nil {
		t.Fatal(err)
	}
	if got := cfg["workspaceFolder"]; got != "/repo" {
		t.Errorf("workspaceFolder = %v", got)
	}
	features, ok := cfg["features"].(map[string]any)
	if !ok || len(features) != 0 {
		t.Errorf("expected empty features map, got %v", cfg["features"])
	}
}

// --- HashDevcontainerJSON / caching (hash stability, hit vs miss) ---

func TestHashDevcontainerJSON_SameInputSameHash(t *testing.T) {
	generated, err := GenerateDevcontainerJSON([]RuntimeLanguage{{ID: "go", Version: "1.26"}}, injectedContract{RepoPath: "/repo"})
	if err != nil {
		t.Fatal(err)
	}
	h1 := HashDevcontainerJSON(generated)
	h2 := HashDevcontainerJSON(generated)
	if h1 != h2 {
		t.Errorf("expected identical hash for identical input, got %q vs %q", h1, h2)
	}
	if h1 == "" {
		t.Error("expected a non-empty hash")
	}
}

// TestHashDevcontainerJSON_LanguageListChangeChangesHash is the cache-miss
// half: a change to the language list must change the generated JSON's hash
// so EnsureDevcontainerRunning's --id-label no longer matches the old
// container and a rebuild is triggered.
func TestHashDevcontainerJSON_LanguageListChangeChangesHash(t *testing.T) {
	before, err := GenerateDevcontainerJSON([]RuntimeLanguage{{ID: "go", Version: "1.25"}}, injectedContract{RepoPath: "/repo"})
	if err != nil {
		t.Fatal(err)
	}
	after, err := GenerateDevcontainerJSON([]RuntimeLanguage{{ID: "go", Version: "1.26"}}, injectedContract{RepoPath: "/repo"})
	if err != nil {
		t.Fatal(err)
	}
	if HashDevcontainerJSON(before) == HashDevcontainerJSON(after) {
		t.Error("expected different hashes for different language lists (cache should miss)")
	}
}

// TestHashDevcontainerJSON_SameInputSameRepoPathIsCacheHit is the cache-hit
// half: calling GenerateDevcontainerJSON twice with the same language list
// and contract (as EnsureDevcontainerRunning does on every call for an
// unchanged repo) must reproduce the same hash, which is what lets
// --id-label find and reuse the existing container instead of rebuilding.
func TestHashDevcontainerJSON_SameInputSameRepoPathIsCacheHit(t *testing.T) {
	contract := injectedContract{RepoPath: "/repo", MCPServerPath: "/opt/mcp"}
	langs := []RuntimeLanguage{{ID: "go", Version: "1.26"}}
	first, err := GenerateDevcontainerJSON(langs, contract)
	if err != nil {
		t.Fatal(err)
	}
	second, err := GenerateDevcontainerJSON(langs, contract)
	if err != nil {
		t.Fatal(err)
	}
	if HashDevcontainerJSON(first) != HashDevcontainerJSON(second) {
		t.Error("expected identical hash for repeated calls with unchanged input (cache should hit)")
	}
}

// TestHashDevcontainerJSON_IndependentOfHostCredentialDirState is the
// regression test for review finding 6 (MEDIUM): the previous implementation
// gated each credential-dir mount on os.Stat, so the generated JSON — and
// therefore its hash — depended on which dirs happened to exist on whichever
// host ran the code, causing spurious rebuilds and hash disagreement between
// the sweeper and the dispatcher when they observed different host state (or
// the same host's state changed between calls). The fix mounts all
// credentialDirs unconditionally, so the hash must be identical for the SAME
// HostHome path whether or not any of its credential dirs actually exist on
// disk. Uses one fixed home path (never created on disk) as a baseline, then
// re-generates after physically creating first some, then all, of
// credentialDirs under it — a test that only called the function twice in a
// row without ever mutating host state would not catch the os.Stat bug this
// guards against.
func TestHashDevcontainerJSON_IndependentOfHostCredentialDirState(t *testing.T) {
	langs := []RuntimeLanguage{{ID: "go", Version: "1.26"}}
	home := t.TempDir() // fixed path for the whole test — only its contents change

	genHash := func() string {
		out, err := GenerateDevcontainerJSON(langs, injectedContract{RepoPath: "/repo", HostHome: home})
		if err != nil {
			t.Fatal(err)
		}
		return HashDevcontainerJSON(out)
	}

	hNoDirs := genHash() // home exists but is empty — no credential dirs on disk yet

	if err := os.MkdirAll(filepath.Join(home, credentialDirs[0]), 0o755); err != nil {
		t.Fatal(err)
	}
	hSomeDirs := genHash()

	for _, dir := range credentialDirs[1:] {
		if err := os.MkdirAll(filepath.Join(home, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	hAllDirs := genHash()

	if hNoDirs != hSomeDirs {
		t.Errorf("hash changed after creating one credential dir on host: %q (none) vs %q (some)", hNoDirs, hSomeDirs)
	}
	if hNoDirs != hAllDirs {
		t.Errorf("hash changed after creating all credential dirs on host: %q (none) vs %q (all)", hNoDirs, hAllDirs)
	}
}

// TestGenerateDevcontainerJSON_OmitsAbsentCredentialDirs pins the corrected
// behavior. This test previously asserted the opposite — that all four dirs
// were mounted unconditionally, to keep the cache key host-independent — and
// that over-correction broke every build on a host without all four providers
// ("bind source path does not exist"). Host-independence of the *hash* is now
// achieved by stripping these entries before hashing instead; see
// TestHashDevcontainerJSON_IgnoresCredentialMounts.
func TestGenerateDevcontainerJSON_OmitsAbsentCredentialDirs(t *testing.T) {
	home := t.TempDir() // no credential dirs created
	out, err := GenerateDevcontainerJSON(nil, injectedContract{RepoPath: "/repo", HostHome: home})
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err := json.Unmarshal([]byte(out), &cfg); err != nil {
		t.Fatal(err)
	}
	mounts := stringSliceField(cfg["mounts"])
	for _, dir := range credentialDirs {
		if containsSubstring(mounts, filepath.Join(home, dir)) {
			t.Errorf("mounted absent credential dir %q; docker run rejects a bind with no source: %v", dir, mounts)
		}
	}
}

// --- ExpectedDevcontainerHash / ExpectedDevcontainerHashFromFile (worktreesweep's reap-decision input) ---

func TestExpectedDevcontainerHash_EmptyLanguagesMeansNoContainerExpected(t *testing.T) {
	m := &RuntimeManager{}
	hash, err := m.ExpectedDevcontainerHash("/repo", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if hash != "" {
		t.Errorf("expected empty hash for no languages configured, got %q", hash)
	}
}

func TestExpectedDevcontainerHash_MatchesEnsureRunningInput(t *testing.T) {
	m := &RuntimeManager{MCPServerPath: "/opt/mcp"}
	langs := []RuntimeLanguage{{ID: "go", Version: "1.26"}}

	hash, err := m.ExpectedDevcontainerHash("/repo", langs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if hash == "" {
		t.Fatal("expected a non-empty hash")
	}

	// Recompute via the same path EnsureDevcontainerRunning uses internally
	// and confirm they agree — this is what makes worktreesweep's staleness
	// check meaningful (comparing against the same value a real
	// EnsureDevcontainerRunning call would have labeled the container with).
	generated, err := m.buildGeneratedDevcontainerJSON("/repo", langs)
	if err != nil {
		t.Fatal(err)
	}
	want := HashDevcontainerJSON(generated)
	if hash != want {
		t.Errorf("ExpectedDevcontainerHash = %q, want %q (mismatch with EnsureDevcontainerRunning's own hash input)", hash, want)
	}
}

func TestExpectedDevcontainerHashFromFile_EmptyMeansNoContainerExpected(t *testing.T) {
	m := &RuntimeManager{}
	if hash := m.ExpectedDevcontainerHashFromFile(""); hash != "" {
		t.Errorf("expected empty hash for empty rawJSON, got %q", hash)
	}
}

func TestExpectedDevcontainerHashFromFile_MatchesRawJSONHash(t *testing.T) {
	m := &RuntimeManager{}
	raw := `{"image":"golang:1.26"}`
	got := m.ExpectedDevcontainerHashFromFile(raw)
	want := HashDevcontainerJSON(raw)
	if got != want {
		t.Errorf("ExpectedDevcontainerHashFromFile = %q, want %q", got, want)
	}
}

// containsSubstring reports whether any element of ss contains substr —
// used above to check for an expected mount/runArg without depending on
// exact list ordering.
func containsSubstring(ss []string, substr string) bool {
	for _, s := range ss {
		if strings.Contains(s, substr) {
			return true
		}
	}
	return false
}

// stringSliceField reads a JSON-decoded field that's expected to be a
// []string, tolerating it being absent (nil interface) or already
// []interface{} (json.Unmarshal's default for a JSON array into
// map[string]any). Test-only: production code (GenerateDevcontainerJSON)
// builds mounts/runArgs directly as []string and never needs to read them
// back out of a decoded map — this exists solely so these tests can inspect
// the round-tripped JSON output.
func stringSliceField(v any) []string {
	if v == nil {
		return nil
	}
	raw, ok := v.([]interface{})
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, e := range raw {
		if s, ok := e.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// TestDevcontainerConfigPath_BasenameIsDevcontainerJSON guards the constraint
// that took this feature down in production: the devcontainer CLI validates
// the config file's *basename*, not just its contents, and rejects anything
// that isn't "devcontainer.json" or ".devcontainer.json" —
//
//	Error: Filename must be devcontainer.json or .devcontainer.json
//	(/tmp/ate-devcontainer-<uuid>.json)
//
// The JSON itself was valid, so nothing upstream caught it. Per-repo
// uniqueness therefore has to live in the containing directory.
func TestDevcontainerConfigPath_BasenameIsDevcontainerJSON(t *testing.T) {
	path := devcontainerConfigPath("577271bc-e2d7-4882-88ff-0a1c19af4aee")
	if got := filepath.Base(path); got != "devcontainer.json" {
		t.Errorf("basename is %q; the CLI only accepts devcontainer.json or .devcontainer.json", got)
	}
	// Uniqueness must still hold, or two repos would race on one config.
	other := devcontainerConfigPath("a-different-repo-id")
	if filepath.Dir(path) == filepath.Dir(other) {
		t.Errorf("two repos share a config dir (%q) — the id must be in the directory", filepath.Dir(path))
	}
}

// TestDevcontainerEnv_DoesNotInheritRootHome guards the second production
// failure of this feature. entrypoint.sh drops root to `node` via su-exec,
// which leaves HOME=/root behind while the process runs as uid 1000. The
// devcontainer CLI shells out to docker, docker tries to read
// /root/.docker/config.json, gets EACCES, and prints a warning to stderr —
// and the CLI treats any stderr from its container lookup as a hard failure:
//
//	Error: Command failed: docker ps -q -a --filter label=ate.repo_id=...
//
// with nothing explaining why. So the subprocess env must set HOME itself
// rather than inheriting it.
func TestDevcontainerEnv_DoesNotInheritRootHome(t *testing.T) {
	t.Setenv("HOME", "/root")

	var home string
	for _, kv := range devcontainerEnv() {
		if after, ok := strings.CutPrefix(kv, "HOME="); ok {
			home = after
		}
	}
	if home == "" {
		t.Fatal("devcontainerEnv must set HOME explicitly; inheriting it breaks docker config loading")
	}
	if home == "/root" {
		t.Errorf("HOME=%q — docker cannot read /root/.docker as the non-root runtime user", home)
	}
}

// TestDevcontainerEnv_PassesPath covers the other half: the CLI resolves
// `docker` by name, so a subprocess env without PATH cannot find it at all.
func TestDevcontainerEnv_PassesPath(t *testing.T) {
	t.Setenv("PATH", "/usr/local/bin:/usr/bin")
	for _, kv := range devcontainerEnv() {
		if kv == "PATH=/usr/local/bin:/usr/bin" {
			return
		}
	}
	t.Error("devcontainerEnv must pass PATH through; the CLI resolves docker by name")
}

// TestGenerateDevcontainerJSON_SkipsMissingCredentialDirs guards the third
// production failure of this feature. Mounting all of credentialDirs
// unconditionally (an over-correction while fixing the cache key) made every
// build fail on any host not using all four providers:
//
//	docker: Error response from daemon: invalid mount config for type
//	"bind": bind source path does not exist: /home/node/.codex
//
// Docker auto-creates a missing source for volumes, not for binds.
func TestGenerateDevcontainerJSON_SkipsMissingCredentialDirs(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o700); err != nil {
		t.Fatal(err)
	}

	out, err := GenerateDevcontainerJSON(
		[]RuntimeLanguage{{ID: "go", Version: "1.26"}},
		injectedContract{RepoPath: "/repo", HostHome: home},
	)
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err := json.Unmarshal([]byte(out), &cfg); err != nil {
		t.Fatal(err)
	}
	mounts := stringSliceField(cfg["mounts"])

	if !containsSubstring(mounts, filepath.Join(home, ".claude")) {
		t.Errorf("existing credential dir was not mounted: %v", mounts)
	}
	for _, missing := range []string{".codex", ".qwen"} {
		if containsSubstring(mounts, filepath.Join(home, missing)) {
			t.Errorf("mounted %s, which does not exist — docker run will reject it: %v", missing, mounts)
		}
	}
}

// TestHashDevcontainerJSON_IgnoresCredentialMounts is the other half: the
// mount list is necessarily host-dependent now, so the hash must not be, or
// configuring a new provider would silently force a rebuild and could leave
// the sweeper and dispatcher disagreeing about a repo's expected hash.
func TestHashDevcontainerJSON_IgnoresCredentialMounts(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o700); err != nil {
		t.Fatal(err)
	}
	langs := []RuntimeLanguage{{ID: "go", Version: "1.26"}}
	contract := injectedContract{RepoPath: "/repo", HostHome: home}

	before, err := GenerateDevcontainerJSON(langs, contract)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate an operator running a codex session, creating ~/.codex.
	if err := os.MkdirAll(filepath.Join(home, ".codex"), 0o700); err != nil {
		t.Fatal(err)
	}
	after, err := GenerateDevcontainerJSON(langs, contract)
	if err != nil {
		t.Fatal(err)
	}

	if before == after {
		t.Fatal("expected the mount list to change; the test is not exercising the property")
	}
	if HashDevcontainerJSON(before) != HashDevcontainerJSON(after) {
		t.Error("hash changed when a credential dir appeared — an unrelated rebuild, and sweeper/dispatcher hash disagreement")
	}
}

// TestGenerateDevcontainerJSON_UsesHostPathsForBindSources guards the fourth
// production failure of this feature, and the one the previous three were
// symptoms of: bind sources are resolved by the Docker daemon on the HOST,
// not inside the backend container. Compose mounts ${HOME}/.claude at
// /home/node/.claude, so emitting the backend's own view of that path gives
// the daemon something it cannot see:
//
//	docker: Error response from daemon: invalid mount config for type
//	"bind": bind source path does not exist: /home/node/.claude
//
// The mount target must stay the in-container path the agent CLI expects.
func TestGenerateDevcontainerJSON_UsesHostPathsForBindSources(t *testing.T) {
	hostHome := t.TempDir()
	if err := os.MkdirAll(filepath.Join(hostHome, ".claude"), 0o700); err != nil {
		t.Fatal(err)
	}

	out, err := GenerateDevcontainerJSON(nil, injectedContract{
		RepoPath:          "/repo",
		MCPServerPath:     "/app/mcp-server",
		HostMCPBindSource: "/opt/host/mcp-server",
		HostHome:          hostHome,
	})
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err := json.Unmarshal([]byte(out), &cfg); err != nil {
		t.Fatal(err)
	}
	mounts := stringSliceField(cfg["mounts"])

	wantCred := "source=" + filepath.Join(hostHome, ".claude") +
		",target=" + RuntimeContainerHome + "/.claude,type=bind"
	if !containsSubstring(mounts, wantCred) {
		t.Errorf("credential mount should use the host source: want %q in %v", wantCred, mounts)
	}
	wantMCP := "source=/opt/host/mcp-server,target=/app/mcp-server,type=bind,readonly"
	if !containsSubstring(mounts, wantMCP) {
		t.Errorf("sidecar mount should bind host source to container target: want %q in %v", wantMCP, mounts)
	}
}

// TestGenerateDevcontainerJSON_OmitsSidecarWithoutHostPath covers the
// containerized default: mcp-server is baked into the backend image at
// /app/mcp-server with no host path, so the mount must be omitted rather than
// emitted with a source the daemon will reject and fail the whole build.
func TestGenerateDevcontainerJSON_OmitsSidecarWithoutHostPath(t *testing.T) {
	out, err := GenerateDevcontainerJSON(nil, injectedContract{
		RepoPath:      "/repo",
		MCPServerPath: "/app/mcp-server",
		// HostMCPBindSource deliberately empty
	})
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err := json.Unmarshal([]byte(out), &cfg); err != nil {
		t.Fatal(err)
	}
	if containsSubstring(stringSliceField(cfg["mounts"]), "mcp-server") {
		t.Errorf("sidecar mounted without a host source: %v", cfg["mounts"])
	}
}
