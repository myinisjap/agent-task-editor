package agent

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

// containerLabelRepo/containerLabelImage tag every container this package
// creates, so it can be found again (docker ps --filter) and swept when its
// repo is gone or its image ref has changed. See RuntimeManager.EnsureRunning
// and worktreesweep's container reaping.
const (
	containerLabelRepo  = "ate.repo_id"
	containerLabelImage = "ate.image"
)

// RuntimeContainerHome is the HOME every runtime container is assumed to
// have — the devcontainer/vscode-image convention verified end-to-end in
// runtime-images.md's spike 2 ("HOME=/home/vscode with ~/.claude mounted
// there works": CLI auth, MCP sidecar round-trip, all passed). Provider
// credential dirs are bind-mounted here from the backend's own home
// directory, and cli.go's spawn() callers override HOME/PATH to match this
// (see the PATH/HOME doc comment on buildDockerRunArgs below) rather than
// forwarding the backend's own HOME/PATH into a different image.
//
// ponytail: fixed value, not introspected per image. Correct for
// devcontainer-convention images (the documented/supported case) but wrong for
// a plain upstream image like golang:1.26, which runs as root with
// HOME=/root — there the credential mounts land somewhere the CLI never reads
// and auth fails with a misleading "not logged in". Upgrade path when that
// matters: read the image's configured user/home once at EnsureRunning time
// (`docker image inspect -f '{{.Config.User}}'`, or `docker exec … sh -c 'echo
// $HOME'` against the started container) and store it on the container record
// alongside the ate.image label, instead of assuming one convention.
const RuntimeContainerHome = "/home/vscode"

// credentialDirs are the provider config directories mounted read-write from
// the backend's own home directory into every runtime container, at the same
// relative path under RuntimeContainerHome. Read-write (not :ro) because the
// claude CLI rotates its OAuth refresh token on use
// (claude_credentials.go) and other CLIs may update their own config/session
// state during a run.
var credentialDirs = []string{".claude", ".claude.json", ".codex", ".qwen"}

// RuntimeManager ensures a long-lived, per-repo docker container is running
// for repos that declare a runtime_image, and returns its name so providers'
// spawn() (providers/cli.go) can `docker exec` into it instead of running the
// CLI in-process. Empty runtime_image is handled entirely by the caller
// (dispatcher.go) never invoking this type — RuntimeManager itself has no
// "no image" case.
//
// One instance is shared across all dispatch goroutines; the per-repo mutex
// returned by lockFor serializes EnsureRunning per repo so two concurrent
// dispatches for the same repo don't race to create duplicate containers
// (docker ps --filter -> docker run is check-then-act).
type RuntimeManager struct {
	// MCPServerPath, if set, is bind-mounted read-only into every runtime
	// container at the same absolute path (config.Config.MCPBinary /
	// MCP_SERVER_PATH) — the sidecar is invoked by absolute path from the
	// generated MCP config JSON, so it must exist at that same path inside
	// the container.
	MCPServerPath string
	// PidsLimit caps the container's total process count
	// (--pids-limit). 0 uses dockerDefaultPidsLimit.
	PidsLimit int

	mu        sync.Mutex
	ensureMus map[string]*sync.Mutex // per-repo lock, keyed by repo id
}

// dockerDefaultPidsLimit is the --pids-limit applied when RuntimeManager
// doesn't specify one — generous enough for a real agentic coding session
// (CLI + Bash + language toolchains) while still bounding a runaway fork
// bomb.
const dockerDefaultPidsLimit = 512

// lockFor returns the per-repo mutex, creating it on first use.
func (m *RuntimeManager) lockFor(repoID string) *sync.Mutex {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.ensureMus == nil {
		m.ensureMus = make(map[string]*sync.Mutex)
	}
	l, ok := m.ensureMus[repoID]
	if !ok {
		l = &sync.Mutex{}
		m.ensureMus[repoID] = l
	}
	return l
}

// containerName is deterministic per repo so repeated EnsureRunning calls
// (and `docker ps --filter name=`) can find the same container without
// tracking any extra state ourselves — docker's own label filter is the
// source of truth, this is just a human-readable name.
func containerName(repoID string) string {
	return "ate-runtime-" + repoID
}

// EnsureRunning returns the name of a running container for repoID, creating
// one (or recreating it) if needed. Safe for concurrent use across repos;
// serialized per repoID so two concurrent dispatches for the same repo can't
// race to create duplicate containers.
func (m *RuntimeManager) EnsureRunning(ctx context.Context, repoID, repoPath, image string) (string, error) {
	if image == "" {
		return "", fmt.Errorf("runtime: EnsureRunning called with empty image for repo %s", repoID)
	}
	lock := m.lockFor(repoID)
	lock.Lock()
	defer lock.Unlock()

	name := containerName(repoID)

	existingImage, running, err := m.inspectExisting(ctx, name)
	if err != nil {
		return "", fmt.Errorf("runtime: inspect existing container: %w", err)
	}
	if running && existingImage == image {
		return name, nil
	}
	if existingImage != "" {
		// Either stopped (crashed/host reboot) or the image ref changed —
		// either way, remove it and recreate below. `docker rm -f` is a
		// no-op-safe way to handle both "stopped" and "running with stale
		// image" in one call.
		if out, err := exec.CommandContext(ctx, "docker", "rm", "-f", name).CombinedOutput(); err != nil {
			return "", fmt.Errorf("runtime: remove stale container %s: %w (%s)", name, err, strings.TrimSpace(string(out)))
		}
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		// No home dir resolvable (unusual for the backend's own process) —
		// degrade to no credential mounts rather than failing the whole
		// container: an agent without provider credentials fails fast and
		// visibly on its first auth check, which is easier to diagnose than
		// EnsureRunning refusing to start the container at all.
		homeDir = ""
	}

	args := buildDockerRunArgs(dockerRunSpec{
		Name:          name,
		Image:         image,
		RepoID:        repoID,
		RepoPath:      repoPath,
		MCPServerPath: m.MCPServerPath,
		HostHome:      homeDir,
		PidsLimit:     m.PidsLimit,
	})
	if out, err := exec.CommandContext(ctx, "docker", args...).CombinedOutput(); err != nil {
		return "", fmt.Errorf("runtime: docker run failed: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return name, nil
}

// inspectExisting looks up a container by name via `docker ps -a --filter
// name=^/<name>$`, returning its ate.image label and whether it's currently
// running. Returns ("", false, nil) if no such container exists.
func (m *RuntimeManager) inspectExisting(ctx context.Context, name string) (image string, running bool, err error) {
	// Anchored name filter (^/<name>$) so e.g. "ate-runtime-repo1" doesn't
	// also match "ate-runtime-repo10".
	out, err := exec.CommandContext(ctx, "docker", "ps", "-a",
		"--filter", "name=^/"+name+"$",
		"--format", "{{.Label \""+containerLabelImage+"\"}}\t{{.State}}").Output()
	if err != nil {
		return "", false, err
	}
	line := strings.TrimSpace(string(out))
	if line == "" {
		return "", false, nil
	}
	// Only the first line matters — the anchored filter should match at
	// most one container.
	line = strings.SplitN(line, "\n", 2)[0]
	parts := strings.SplitN(line, "\t", 2)
	image = parts[0]
	if len(parts) > 1 {
		running = parts[1] == "running"
	}
	return image, running, nil
}

// dockerRunSpec is the pure input to buildDockerRunArgs, kept separate from
// RuntimeManager so the argv construction is unit-testable without a Docker
// daemon.
type dockerRunSpec struct {
	Name          string
	Image         string
	RepoID        string
	RepoPath      string
	MCPServerPath string
	// HostHome is the backend process's own home directory (os.UserHomeDir),
	// used as the source side of the credentialDirs mounts below. Empty
	// skips credential mounting entirely (see EnsureRunning).
	HostHome  string
	PidsLimit int
}

// buildDockerRunArgs constructs the `docker run` argv that starts a repo's
// long-lived runtime container. The container is kept alive with `sleep
// infinity` under --init (proper zombie reaping for a PID-1-less sleep loop);
// spawn() (providers/cli.go) then `docker exec`s into it per task run.
//
// Mounts:
//   - repo root and /tmp, all same-path (see runtime-images.md's spike 1
//     finding): a git worktree's `.git` file holds an absolute gitdir
//     pointer back to the main clone, so the repo root must land at the
//     exact same path inside the container as on the host, and /tmp must
//     too (MCP config + RESULT_FILE handoff — see providers/mcp.go).
//   - the MCP sidecar binary, read-only, at its own path — the generated MCP
//     config JSON references it by absolute path.
//   - each of credentialDirs, read-write, from HostHome/<dir> (the backend's
//     own credential store — the same ~/.claude a non-containerized run
//     already reads) to RuntimeContainerHome/<dir>. A source path that
//     doesn't exist on the host (e.g. no ~/.codex because this deployment
//     only uses claude) is silently skipped — mounting a nonexistent host
//     path would otherwise make `docker run` create an empty directory
//     there, masking a real "provider not configured" state as an empty
//     credential dir instead of a missing one.
//
// See RuntimeContainerHome's doc comment for why HOME is fixed at
// /home/vscode rather than introspected per-image, and cli.go's spawn()
// callers for the corresponding env-side decision (HOME/PATH are set to the
// container's known values, not forwarded from the backend's own
// environment — see providers/cli.go's containerEnvOverrides).
func buildDockerRunArgs(spec dockerRunSpec) []string {
	pidsLimit := spec.PidsLimit
	if pidsLimit <= 0 {
		pidsLimit = dockerDefaultPidsLimit
	}

	args := []string{
		"run", "-d",
		"--name", spec.Name,
		"--label", containerLabelRepo + "=" + spec.RepoID,
		"--label", containerLabelImage + "=" + spec.Image,
		"--init",
		"--security-opt", "no-new-privileges",
		"--cap-drop", "ALL",
		"--pids-limit", fmt.Sprintf("%d", pidsLimit),
		"-v", spec.RepoPath + ":" + spec.RepoPath,
		"-v", "/tmp:/tmp",
	}
	if spec.MCPServerPath != "" {
		args = append(args, "-v", spec.MCPServerPath+":"+spec.MCPServerPath+":ro")
	}
	if spec.HostHome != "" {
		for _, dir := range credentialDirs {
			src := filepath.Join(spec.HostHome, dir)
			if _, err := os.Stat(src); err != nil {
				continue // not configured on this host — see doc comment above
			}
			dst := RuntimeContainerHome + "/" + dir
			args = append(args, "-v", src+":"+dst)
		}
	}
	args = append(args, spec.Image, "sleep", "infinity")
	return args
}

// RemoveContainer force-removes a repo's runtime container, if any. Used by
// worktreesweep's reaping pass when a repo is deleted or its runtime_image
// has changed (the stale container is torn down there rather than left for
// the next EnsureRunning call, since a deleted repo will never call
// EnsureRunning again). Never errors on "no such container" — that's the
// desired end state, not a failure.
func RemoveContainer(ctx context.Context, name string) error {
	out, err := exec.CommandContext(ctx, "docker", "rm", "-f", name).CombinedOutput()
	if err != nil && !bytes.Contains(out, []byte("No such container")) {
		return fmt.Errorf("docker rm -f %s: %w (%s)", name, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// ListManagedContainers returns the name and ate.image/ate.repo_id/ate.dcjson
// labels of every container this package created (i.e. carrying
// containerLabelRepo), for worktreesweep's reaping pass to compare against
// the live repo set.
func ListManagedContainers(ctx context.Context) ([]ManagedContainer, error) {
	out, err := exec.CommandContext(ctx, "docker", "ps", "-a",
		"--filter", "label="+containerLabelRepo,
		"--format", "{{.Names}}\t{{.Label \""+containerLabelRepo+"\"}}\t{{.Label \""+containerLabelImage+"\"}}\t{{.Label \""+devcontainerLabel+"\"}}").Output()
	if err != nil {
		return nil, fmt.Errorf("docker ps: %w", err)
	}
	var containers []ManagedContainer
	for _, line := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 4)
		if len(parts) < 2 {
			continue
		}
		c := ManagedContainer{Name: parts[0], RepoID: parts[1]}
		if len(parts) > 2 {
			c.Image = parts[2]
		}
		if len(parts) > 3 {
			c.DevcontainerHash = parts[3]
		}
		containers = append(containers, c)
	}
	return containers, nil
}

// ManagedContainer is one container discovered by ListManagedContainers.
type ManagedContainer struct {
	Name   string
	RepoID string
	Image  string
	// DevcontainerHash is the container's ate.dcjson label value, if any —
	// set only for containers created via EnsureDevcontainerRunning
	// (devcontainer.go), empty for explicit-runtime_image containers.
	DevcontainerHash string
}

// dockerAvailable reports whether the docker CLI is on PATH — used to skip
// EnsureRunning's daemon-dependent tests in environments without Docker
// (mirrors testing.Short() guards elsewhere in this codebase for
// daemon-dependent tests).
func dockerAvailable() bool {
	_, err := exec.LookPath("docker")
	return err == nil
}
