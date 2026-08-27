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
		"ghcr.io/example/runtime:1", "sleep", "infinity",
	}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("buildDockerRunArgs() = %v, want %v", args, want)
	}
}

func TestBuildDockerRunArgs_WithMCPServerPath(t *testing.T) {
	args := buildDockerRunArgs(dockerRunSpec{
		Name:              "ate-runtime-repo1",
		Image:             "img",
		RepoID:            "repo1",
		RepoPath:          "/repo",
		MCPServerPath:     "/opt/ate/mcp-server",
		HostMCPBindSource: "/opt/ate/mcp-server",
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
	// The name is the only handle EnsureRunning hands back to the dispatcher,
	// and the sweeper reaps by it — so it has to be stable for a repo across
	// calls and never collide between repos. Asserting against a literal
	// rather than a second call, which would be a tautology.
	if got, want := containerName("abc"), "ate-runtime-abc"; got != want {
		t.Errorf("containerName(%q) = %q, want %q", "abc", got, want)
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

// TestIsNameConflictError covers the "someone else won the race to create
// this container" detection used by EnsureRunning to recover from a
// concurrent `docker run` instead of failing the run outright.
func TestIsNameConflictError(t *testing.T) {
	cases := []struct {
		name string
		out  string
		want bool
	}{
		{
			name: "docker name conflict message",
			out:  `docker: Error response from daemon: Conflict. The container name "/ate-runtime-repo1" is already in use by container "abc123". You have to remove (or rename) that container to be able to reuse that name.`,
			want: true,
		},
		{
			name: "unrelated docker error",
			out:  "docker: Error response from daemon: pull access denied for ghcr.io/example/runtime, repository does not exist or may require 'docker login'",
			want: false,
		},
		{
			name: "empty output",
			out:  "",
			want: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isNameConflictError([]byte(tc.out)); got != tc.want {
				t.Errorf("isNameConflictError(%q) = %v, want %v", tc.out, got, tc.want)
			}
		})
	}
}

// --- parseInspectOutput (pure string parsing, no Docker daemon needed) ---

// TestParseInspectOutput covers inspectExisting's `docker ps --format`
// output parsing: empty output (no matching container), a running
// container, a stopped one, a container missing its image label, and
// defensive handling of unexpected multi-line output.
func TestParseInspectOutput(t *testing.T) {
	cases := []struct {
		name        string
		out         string
		wantImage   string
		wantRunning bool
	}{
		{
			name:        "empty output means no container",
			out:         "",
			wantImage:   "",
			wantRunning: false,
		},
		{
			name:        "whitespace-only output means no container",
			out:         "\n",
			wantImage:   "",
			wantRunning: false,
		},
		{
			name:        "running container",
			out:         "ghcr.io/example/runtime:1\trunning\n",
			wantImage:   "ghcr.io/example/runtime:1",
			wantRunning: true,
		},
		{
			name:        "stopped container",
			out:         "ghcr.io/example/runtime:1\texited\n",
			wantImage:   "ghcr.io/example/runtime:1",
			wantRunning: false,
		},
		{
			// TrimSpace strips the leading tab along with the trailing
			// newline, so a blank image label collapses the line to a
			// single field: it's read as the image, and running (no second
			// field) defaults to false. This documents parseInspectOutput's
			// actual behavior for this edge case rather than an ideal one.
			name:        "missing image label",
			out:         "\trunning\n",
			wantImage:   "running",
			wantRunning: false,
		},
		{
			name:        "multi-line output uses only the first line",
			out:         "ghcr.io/example/runtime:1\trunning\nghcr.io/example/runtime:2\texited\n",
			wantImage:   "ghcr.io/example/runtime:1",
			wantRunning: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			image, running, err := parseInspectOutput(tc.out)
			if err != nil {
				t.Fatalf("parseInspectOutput(%q) returned error: %v", tc.out, err)
			}
			if image != tc.wantImage || running != tc.wantRunning {
				t.Errorf("parseInspectOutput(%q) = (%q, %v), want (%q, %v)", tc.out, image, running, tc.wantImage, tc.wantRunning)
			}
		})
	}
}
