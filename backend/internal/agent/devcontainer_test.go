package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --- ResolveDevcontainerSource (resolution-order precedence) ---
//
// The full four-case resolution order from runtime-images.md is:
//  1. repos.runtime_image (explicit ref) — handled entirely by the caller
//     (dispatcher.go) before ResolveDevcontainerSource is ever invoked; not
//     this function's concern.
//  2. .devcontainer/devcontainer.json committed in the repo.
//  3. repos.devcontainer_json (DB-stored).
//  4. None of the above -> in-process.
//
// ResolveDevcontainerSource covers cases 2-4; case 1 is exercised at the
// dispatcher level by TestE2E_RuntimeImageSkipsDevcontainerResolution and
// TestE2E_DevcontainerConfigWithoutManagerEscalates in
// runtime_dispatch_e2e_test.go (a real explicit RuntimeImage never even
// reaches this function, and a devcontainer source with no RuntimeManager
// escalates the same way an explicit runtime_image does).
func TestResolveDevcontainerSource(t *testing.T) {
	cases := []struct {
		name         string
		repoFileJSON string
		dbJSON       string
		wantSource   devcontainerSource
		wantRaw      string
	}{
		{
			name:         "repo file wins over DB",
			repoFileJSON: `{"image":"from-repo-file"}`,
			dbJSON:       `{"image":"from-db"}`,
			wantSource:   devcontainerRepoFile,
			wantRaw:      `{"image":"from-repo-file"}`,
		},
		{
			name:         "DB used when no repo file",
			repoFileJSON: "",
			dbJSON:       `{"image":"from-db"}`,
			wantSource:   devcontainerDB,
			wantRaw:      `{"image":"from-db"}`,
		},
		{
			name:         "neither set: none (in-process)",
			repoFileJSON: "",
			dbJSON:       "",
			wantSource:   devcontainerNone,
			wantRaw:      "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotSource, gotRaw := ResolveDevcontainerSource(tc.repoFileJSON, tc.dbJSON)
			if gotSource != tc.wantSource {
				t.Errorf("source = %v, want %v", gotSource, tc.wantSource)
			}
			if gotRaw != tc.wantRaw {
				t.Errorf("raw = %q, want %q", gotRaw, tc.wantRaw)
			}
		})
	}
}

// TestResolveDevcontainerSource_NoneMeansInProcess locks in that an empty
// result from ResolveDevcontainerSource is exactly what dispatcher.go treats
// as "run in-process" (runtimeContainer stays "" — see startRun): calling
// EnsureDevcontainerRunning is conditioned on source != devcontainerNone in
// the caller, so devcontainerNone must always pair with an empty raw JSON,
// never a non-empty one a caller might mistakenly still try to build from.
func TestResolveDevcontainerSource_NoneMeansInProcess(t *testing.T) {
	source, raw := ResolveDevcontainerSource("", "")
	if source != devcontainerNone {
		t.Fatalf("expected devcontainerNone, got %v", source)
	}
	if raw != "" {
		t.Fatalf("expected empty raw JSON for devcontainerNone, got %q", raw)
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

// --- BuildEffectiveDevcontainerJSON (injected mount/hardening contract) ---

// TestBuildEffectiveDevcontainerJSON_EmptyRawStillInjectsContract confirms
// that even a from-scratch config (no repo file, no DB config — the
// UI-authored-from-nothing case) gets the full mandatory contract.
func TestBuildEffectiveDevcontainerJSON_EmptyRawStillInjectsContract(t *testing.T) {
	out, err := BuildEffectiveDevcontainerJSON("", injectedContract{
		RepoPath:      "/data/repos/myrepo",
		MCPServerPath: "/opt/ate/mcp-server",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var cfg map[string]any
	if err := json.Unmarshal([]byte(out), &cfg); err != nil {
		t.Fatalf("effective JSON did not parse: %v", err)
	}

	if got := cfg["workspaceMount"]; got != "source=/data/repos/myrepo,target=/data/repos/myrepo,type=bind" {
		t.Errorf("workspaceMount = %v", got)
	}
	if got := cfg["workspaceFolder"]; got != "/data/repos/myrepo" {
		t.Errorf("workspaceFolder = %v", got)
	}

	mounts := stringSliceField(cfg["mounts"])
	if !containsSubstring(mounts, "source=/tmp,target=/tmp,type=bind") {
		t.Errorf("expected /tmp mount in %v", mounts)
	}
	if !containsSubstring(mounts, "source=/opt/ate/mcp-server,target=/opt/ate/mcp-server,type=bind,readonly") {
		t.Errorf("expected read-only MCP sidecar mount in %v", mounts)
	}

	runArgs := stringSliceField(cfg["runArgs"])
	if !containsSubstring(runArgs, "--security-opt=no-new-privileges") {
		t.Errorf("expected no-new-privileges in runArgs %v", runArgs)
	}
	if !containsSubstring(runArgs, "--cap-drop=ALL") {
		t.Errorf("expected cap-drop=ALL in runArgs %v", runArgs)
	}
}

// TestBuildEffectiveDevcontainerJSON_PreservesUserFieldsAndAppendsMounts
// verifies the merge never destroys a user-supplied value for a key it
// doesn't itself own (e.g. "features", "image"), and appends to (rather
// than replacing) a user-supplied mounts/runArgs list.
func TestBuildEffectiveDevcontainerJSON_PreservesUserFieldsAndAppendsMounts(t *testing.T) {
	raw := `{
		"image": "mcr.microsoft.com/devcontainers/base:ubuntu",
		"features": {"ghcr.io/devcontainers/features/go:1": {"version": "1.26"}},
		"mounts": ["source=/extra,target=/extra,type=bind"],
		"runArgs": ["--env=FOO=bar"]
	}`
	out, err := BuildEffectiveDevcontainerJSON(raw, injectedContract{RepoPath: "/repo"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var cfg map[string]any
	if err := json.Unmarshal([]byte(out), &cfg); err != nil {
		t.Fatalf("effective JSON did not parse: %v", err)
	}

	if cfg["image"] != "mcr.microsoft.com/devcontainers/base:ubuntu" {
		t.Errorf("expected user-supplied image preserved, got %v", cfg["image"])
	}
	if _, ok := cfg["features"]; !ok {
		t.Errorf("expected user-supplied features preserved")
	}

	mounts := stringSliceField(cfg["mounts"])
	if !containsSubstring(mounts, "source=/extra,target=/extra,type=bind") {
		t.Errorf("expected user's own mount preserved in %v", mounts)
	}
	if !containsSubstring(mounts, "source=/tmp,target=/tmp,type=bind") {
		t.Errorf("expected injected /tmp mount alongside user's own in %v", mounts)
	}

	runArgs := stringSliceField(cfg["runArgs"])
	if !containsSubstring(runArgs, "--env=FOO=bar") {
		t.Errorf("expected user's own runArg preserved in %v", runArgs)
	}
	if !containsSubstring(runArgs, "--cap-drop=ALL") {
		t.Errorf("expected injected hardening runArg alongside user's own in %v", runArgs)
	}
}

// TestBuildEffectiveDevcontainerJSON_CredentialMountsOnlyForExistingDirs
// mirrors TestBuildDockerRunArgs_CredentialMountsOnlyForExistingDirs
// (runtime_test.go) for the devcontainer path: credentialDirs are mounted
// only when present on the host, so an unconfigured provider doesn't get an
// empty directory masking "not configured" as "empty credentials".
func TestBuildEffectiveDevcontainerJSON_CredentialMountsOnlyForExistingDirs(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}

	out, err := BuildEffectiveDevcontainerJSON("", injectedContract{
		RepoPath: "/repo",
		HostHome: home,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var cfg map[string]any
	if err := json.Unmarshal([]byte(out), &cfg); err != nil {
		t.Fatalf("effective JSON did not parse: %v", err)
	}
	mounts := stringSliceField(cfg["mounts"])
	wantMount := "source=" + filepath.Join(home, ".claude") + ",target=" + RuntimeContainerHome + "/.claude,type=bind"
	if !containsSubstring(mounts, wantMount) {
		t.Errorf("expected %q in mounts %v", wantMount, mounts)
	}
	for _, m := range mounts {
		if strings.Contains(m, ".codex") || strings.Contains(m, ".qwen") {
			t.Errorf("expected no mount for nonexistent credential dirs, got %v", mounts)
		}
	}
}

func TestBuildEffectiveDevcontainerJSON_InvalidJSON(t *testing.T) {
	_, err := BuildEffectiveDevcontainerJSON("{not valid json", injectedContract{RepoPath: "/repo"})
	if err == nil {
		t.Fatal("expected an error for malformed input JSON")
	}
}

// --- HashDevcontainerJSON / caching (hash stability, hit vs miss) ---

func TestHashDevcontainerJSON_SameInputSameHash(t *testing.T) {
	effective, err := BuildEffectiveDevcontainerJSON(`{"image":"golang:1.26"}`, injectedContract{RepoPath: "/repo"})
	if err != nil {
		t.Fatal(err)
	}
	h1 := HashDevcontainerJSON(effective)
	h2 := HashDevcontainerJSON(effective)
	if h1 != h2 {
		t.Errorf("expected identical hash for identical input, got %q vs %q", h1, h2)
	}
	if h1 == "" {
		t.Error("expected a non-empty hash")
	}
}

// TestHashDevcontainerJSON_ConfigChangeChangesHash is the cache-miss half of
// the required "cache hit vs miss on hash change" coverage: a change to the
// *source* JSON (e.g. the UI-authored devcontainer_json, or an edit to the
// repo-committed file) must change the effective JSON's hash so
// EnsureDevcontainerRunning's --id-label no longer matches the old
// container and a rebuild is triggered.
func TestHashDevcontainerJSON_ConfigChangeChangesHash(t *testing.T) {
	before, err := BuildEffectiveDevcontainerJSON(`{"image":"golang:1.25"}`, injectedContract{RepoPath: "/repo"})
	if err != nil {
		t.Fatal(err)
	}
	after, err := BuildEffectiveDevcontainerJSON(`{"image":"golang:1.26"}`, injectedContract{RepoPath: "/repo"})
	if err != nil {
		t.Fatal(err)
	}
	if HashDevcontainerJSON(before) == HashDevcontainerJSON(after) {
		t.Error("expected different hashes for different source config (cache should miss)")
	}
}

// TestHashDevcontainerJSON_SameSourceSameRepoPathIsCacheHit is the cache-hit
// half: calling BuildEffectiveDevcontainerJSON twice with the same source
// JSON and the same contract (as EnsureDevcontainerRunning does on every
// call for an unchanged repo) must reproduce the same hash, which is what
// lets --id-label find and reuse the existing container instead of
// rebuilding.
func TestHashDevcontainerJSON_SameSourceSameRepoPathIsCacheHit(t *testing.T) {
	contract := injectedContract{RepoPath: "/repo", MCPServerPath: "/opt/mcp"}
	first, err := BuildEffectiveDevcontainerJSON(`{"image":"golang:1.26"}`, contract)
	if err != nil {
		t.Fatal(err)
	}
	second, err := BuildEffectiveDevcontainerJSON(`{"image":"golang:1.26"}`, contract)
	if err != nil {
		t.Fatal(err)
	}
	if HashDevcontainerJSON(first) != HashDevcontainerJSON(second) {
		t.Error("expected identical hash for repeated calls with unchanged source+contract (cache should hit)")
	}
}

// --- ExpectedDevcontainerHash (worktreesweep's reap-decision input) ---

func TestExpectedDevcontainerHash_EmptyRawJSONMeansNoContainerExpected(t *testing.T) {
	m := &RuntimeManager{}
	hash, err := m.ExpectedDevcontainerHash("/repo", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if hash != "" {
		t.Errorf("expected empty hash for no devcontainer source, got %q", hash)
	}
}

func TestExpectedDevcontainerHash_NonEmptyRawJSONMatchesEnsureRunningInput(t *testing.T) {
	m := &RuntimeManager{MCPServerPath: "/opt/mcp"}
	raw := `{"image":"golang:1.26"}`

	hash, err := m.ExpectedDevcontainerHash("/repo", raw)
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
	effective, err := m.buildEffectiveDevcontainerJSON("/repo", raw)
	if err != nil {
		t.Fatal(err)
	}
	want := HashDevcontainerJSON(effective)
	if hash != want {
		t.Errorf("ExpectedDevcontainerHash = %q, want %q (mismatch with EnsureDevcontainerRunning's own hash input)", hash, want)
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
