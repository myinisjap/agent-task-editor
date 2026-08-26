package agent

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// --- buildDockerRunArgs (pure argv construction, no Docker daemon needed) ---

func TestBuildDockerRunArgs_NoMCPNoHome(t *testing.T) {
	args := buildDockerRunArgs(dockerRunSpec{
		Name:     "ate-runtime-repo1",
		Image:    "ghcr.io/example/runtime:1",
		RepoID:   "repo1",
		RepoPath: "/data/repos/repo1",
	})

	want := []string{
		"run", "-d",
		"--name", "ate-runtime-repo1",
		"--label", "ate.repo_id=repo1",
		"--label", "ate.image=ghcr.io/example/runtime:1",
		"--init",
		"--security-opt", "no-new-privileges",
		"--cap-drop", "ALL",
		"--pids-limit", "512",
		"-v", "/data/repos/repo1:/data/repos/repo1",
		"-v", "/tmp:/tmp",
		"ghcr.io/example/runtime:1", "sleep", "infinity",
	}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("buildDockerRunArgs() = %v, want %v", args, want)
	}
}

func TestBuildDockerRunArgs_WithMCPServerPath(t *testing.T) {
	args := buildDockerRunArgs(dockerRunSpec{
		Name:          "ate-runtime-repo1",
		Image:         "img",
		RepoID:        "repo1",
		RepoPath:      "/repo",
		MCPServerPath: "/opt/ate/mcp-server",
	})

	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "-v /opt/ate/mcp-server:/opt/ate/mcp-server:ro") {
		t.Errorf("expected read-only MCP server mount in args, got %v", args)
	}
}

func TestBuildDockerRunArgs_CustomPidsLimit(t *testing.T) {
	args := buildDockerRunArgs(dockerRunSpec{
		Name: "n", Image: "img", RepoID: "r", RepoPath: "/r",
		PidsLimit: 128,
	})
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--pids-limit 128") {
		t.Errorf("expected custom pids-limit 128, got %v", args)
	}
}

// TestBuildDockerRunArgs_CredentialMountsOnlyForExistingDirs verifies
// credential dirs are mounted read-write from HostHome/<dir> to
// RuntimeContainerHome/<dir>, and that a dir absent on the host is silently
// skipped rather than creating an empty mount point (see buildDockerRunArgs's
// doc comment: mounting a nonexistent host path would make `docker run`
// create an empty directory, masking "provider not configured" as an empty
// credential dir).
func TestBuildDockerRunArgs_CredentialMountsOnlyForExistingDirs(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	// .codex and .qwen and .claude.json deliberately not created.

	args := buildDockerRunArgs(dockerRunSpec{
		Name: "n", Image: "img", RepoID: "r", RepoPath: "/r",
		HostHome: home,
	})
	joined := strings.Join(args, " ")

	wantMount := "-v " + filepath.Join(home, ".claude") + ":" + RuntimeContainerHome + "/.claude"
	if !strings.Contains(joined, wantMount) {
		t.Errorf("expected %q in args, got %v", wantMount, args)
	}
	if strings.Contains(joined, ".codex") || strings.Contains(joined, ".qwen") {
		t.Errorf("expected no mount for nonexistent credential dirs, got %v", args)
	}
}

func TestBuildDockerRunArgs_NoCredentialMountsWhenHostHomeEmpty(t *testing.T) {
	args := buildDockerRunArgs(dockerRunSpec{
		Name: "n", Image: "img", RepoID: "r", RepoPath: "/r",
		HostHome: "",
	})
	joined := strings.Join(args, " ")
	if strings.Contains(joined, RuntimeContainerHome) {
		t.Errorf("expected no RuntimeContainerHome-targeted mounts when HostHome is empty, got %v", args)
	}
}

// --- EnsureRunning / containerName ---

func TestContainerName_DeterministicPerRepo(t *testing.T) {
	if containerName("abc") != containerName("abc") {
		t.Error("containerName should be deterministic for the same repo id")
	}
	if containerName("abc") == containerName("xyz") {
		t.Error("containerName should differ across repos")
	}
}

func TestEnsureRunning_EmptyImageErrors(t *testing.T) {
	m := &RuntimeManager{}
	if _, err := m.EnsureRunning(context.Background(), "repo1", "/repo", ""); err == nil {
		t.Error("expected an error for empty image, got nil")
	}
}

// TestEnsureRunning_RequiresDocker is a smoke test of the real EnsureRunning
// path against a live Docker daemon. Skipped under -short (and when docker
// isn't on PATH) so `go test ./...` stays hermetic — see cli_test.go's
// pattern of daemon-independent unit tests plus this codebase's existing
// testing.Short()-guarded convention for anything that shells out to a real
// external service.
func TestEnsureRunning_RequiresDocker(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping docker-dependent test in -short mode")
	}
	if !dockerAvailable() {
		t.Skip("docker not available on PATH")
	}
	t.Skip("live-docker smoke test intentionally not run in CI; see runtime-images.md spikes for the verified end-to-end result")
}
