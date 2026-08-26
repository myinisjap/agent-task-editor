package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// devcontainerLabel tags a devcontainer-CLI-managed runtime container with
// the sha256 of the effective devcontainer.json it was built from, the same
// way ate.image already tags an explicit-runtime_image container (see
// runtime.go). EnsureDevcontainerRunning compares this label against a
// freshly computed hash to decide whether a rebuild is needed;
// worktreesweep's reaping pass compares it against each repo's
// currently-resolved effective JSON the same way it already does for
// ate.image.
const devcontainerLabel = "ate.dcjson"

// devcontainerFilePath is where a repo-committed devcontainer.json lives,
// relative to the repo root — the standard location every devcontainer-CLI
// consumer (VS Code, Codespaces, JetBrains, Zed) already expects.
const devcontainerFilePath = ".devcontainer/devcontainer.json"

// devcontainerSource identifies which of the three possible inputs produced
// the effective devcontainer.json used to build a repo's runtime container.
// Mirrors the resolution order documented in runtime-images.md: an explicit
// runtime_image always wins over any devcontainer.json, so it isn't a
// devcontainerSource value at all — ResolveDevcontainerSource is only
// called once the caller already knows runtime_image is empty.
type devcontainerSource int

const (
	// devcontainerNone means neither a repo-committed devcontainer.json nor
	// a DB-stored one is configured — the repo runs in-process, exactly as
	// today.
	devcontainerNone devcontainerSource = iota
	// devcontainerRepoFile means .devcontainer/devcontainer.json is
	// committed in the repo. Wins over the DB-stored config.
	devcontainerRepoFile
	// devcontainerDB means repos.devcontainer_json (UI-authored) is used,
	// because the repo has no committed devcontainer.json of its own.
	devcontainerDB
)

// ResolveDevcontainerSource implements steps 2-4 of the resolution order in
// runtime-images.md (step 1, repos.runtime_image, is handled entirely by the
// caller — dispatcher.go only reaches this function once runtime_image is
// already known to be empty). repoFileJSON is the contents of
// .devcontainer/devcontainer.json if the repo has one committed, else "".
// dbJSON is repos.devcontainer_json. Returns the winning source and its raw
// JSON text; devcontainerNone returns ("", devcontainerNone).
//
// Pure and hermetic — takes the repo file's contents as a parameter rather
// than reading the filesystem itself, so resolution-order precedence is
// unit-testable without a real repo checkout.
func ResolveDevcontainerSource(repoFileJSON, dbJSON string) (devcontainerSource, string) {
	if repoFileJSON != "" {
		return devcontainerRepoFile, repoFileJSON
	}
	if dbJSON != "" {
		return devcontainerDB, dbJSON
	}
	return devcontainerNone, ""
}

// ReadRepoDevcontainerFile reads .devcontainer/devcontainer.json from a repo
// checkout, returning "" (no error) if the repo simply doesn't have one —
// that's the common case, not a failure. A real read error (permissions,
// I/O) is returned so the caller can decide how to treat it rather than
// silently falling through to the DB-stored config.
func ReadRepoDevcontainerFile(repoPath string) (string, error) {
	data, err := os.ReadFile(filepath.Join(repoPath, devcontainerFilePath))
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("read %s: %w", devcontainerFilePath, err)
	}
	return string(data), nil
}

// injectedContract builds the mount/hardening contract every devcontainer
// build must carry, regardless of source — see BuildEffectiveDevcontainerJSON.
type injectedContract struct {
	RepoPath      string
	MCPServerPath string
	HostHome      string
}

// BuildEffectiveDevcontainerJSON parses rawJSON (a devcontainer.json from
// either source in ResolveDevcontainerSource) and merges in the mount and
// hardening contract this codebase depends on, without ever mutating a
// user-supplied value the picker/repo file already set for the *same* key
// (mounts/runArgs are appended to, not replaced, since a devcontainer.json
// may legitimately declare its own). Returns the effective JSON, marshaled
// with sorted keys so BuildEffectiveDevcontainerJSON is deterministic and
// HashDevcontainerJSON is stable across process restarts for identical
// input.
//
// Contract injected (see runtime-images.md's mount table and spike
// results):
//   - workspaceMount + workspaceFolder pinned to the same absolute
//     repoPath on both sides — a git worktree's `.git` is a file holding an
//     absolute gitdir pointer, so the repo must land at the exact same path
//     inside the container as on the host (verified in spike 1).
//   - /tmp bound at /tmp (MCP config + RESULT_FILE handoff, see
//     providers/mcp.go).
//   - the MCP sidecar binary, read-only, at its own absolute path (the
//     generated MCP config JSON references it by absolute path).
//   - each of credentialDirs, read-write, from HostHome/<dir> to
//     RuntimeContainerHome/<dir> — same source/target convention as
//     buildDockerRunArgs; a dir absent on the host is silently skipped for
//     the same reason documented there.
//   - runArgs carrying this codebase's hardening flags
//     (--security-opt=no-new-privileges, --cap-drop=ALL), overriding the
//     devcontainer CLI's own defaults (--cap-add SYS_PTRACE --security-opt
//     seccomp=unconfined — see runtime-images.md's spike 1 notes).
//
// When rawJSON came from a repo-committed file, the caller must treat the
// return value as a *copy* to build from — this function never writes back
// to rawJSON's source, so the committed file itself is never modified.
func BuildEffectiveDevcontainerJSON(rawJSON string, contract injectedContract) (string, error) {
	cfg := map[string]any{}
	if strings.TrimSpace(rawJSON) != "" {
		if err := json.Unmarshal([]byte(rawJSON), &cfg); err != nil {
			return "", fmt.Errorf("parse devcontainer.json: %w", err)
		}
	}

	cfg["workspaceMount"] = fmt.Sprintf(
		"source=%s,target=%s,type=bind", contract.RepoPath, contract.RepoPath,
	)
	cfg["workspaceFolder"] = contract.RepoPath

	mounts := stringSliceField(cfg["mounts"])
	mounts = append(mounts, "source=/tmp,target=/tmp,type=bind")
	if contract.MCPServerPath != "" {
		mounts = append(mounts, fmt.Sprintf(
			"source=%s,target=%s,type=bind,readonly", contract.MCPServerPath, contract.MCPServerPath,
		))
	}
	if contract.HostHome != "" {
		for _, dir := range credentialDirs {
			src := filepath.Join(contract.HostHome, dir)
			if _, err := os.Stat(src); err != nil {
				continue // not configured on this host — see buildDockerRunArgs's doc comment
			}
			dst := RuntimeContainerHome + "/" + dir
			mounts = append(mounts, fmt.Sprintf("source=%s,target=%s,type=bind", src, dst))
		}
	}
	cfg["mounts"] = mounts

	runArgs := stringSliceField(cfg["runArgs"])
	runArgs = append(runArgs, "--security-opt=no-new-privileges", "--cap-drop=ALL")
	cfg["runArgs"] = runArgs

	out, err := marshalSorted(cfg)
	if err != nil {
		return "", fmt.Errorf("marshal effective devcontainer.json: %w", err)
	}
	return out, nil
}

// stringSliceField reads a JSON-decoded field that's expected to be a
// []string, tolerating it being absent (nil interface) or already
// []interface{} (json.Unmarshal's default for a JSON array into map[string]any).
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

// marshalSorted JSON-encodes v with map keys in sorted order, so identical
// logical content always produces byte-identical output — required for
// HashDevcontainerJSON to be a stable cache key across process restarts
// (Go's encoding/json already sorts map[string]any keys on marshal, but this
// helper makes that guarantee explicit rather than relying on stdlib
// implementation detail without a test locking it in).
func marshalSorted(cfg map[string]any) (string, error) {
	keys := make([]string, 0, len(cfg))
	for k := range cfg {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	// encoding/json marshals map[string]any with sorted keys already; this
	// round-trip through a plain Marshal is sufficient given that guarantee,
	// but ranging keys in sorted order above documents/enforces the
	// assumption rather than leaving it implicit.
	b, err := json.Marshal(cfg)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// HashDevcontainerJSON returns the sha256 hex digest of effectiveJSON, used
// as both the cache key (stored on the container as the devcontainerLabel
// label) and the input to deciding whether a rebuild is needed.
func HashDevcontainerJSON(effectiveJSON string) string {
	sum := sha256.Sum256([]byte(effectiveJSON))
	return hex.EncodeToString(sum[:])
}

// devcontainerUpResult is the subset of `devcontainer up`'s JSON stdout this
// package needs — verified shape (runtime-images.md spike 1):
// {"outcome":"success","containerId":"...","remoteUser":"vscode","remoteWorkspaceFolder":"..."}.
type devcontainerUpResult struct {
	Outcome     string `json:"outcome"`
	ContainerID string `json:"containerId"`
	Message     string `json:"message"`
}

// runDevcontainerUp shells out to `devcontainer up --workspace-folder
// <repoPath> --config <configPath> --id-label ate.repo_id=<repoID> --id-label
// ate.dcjson=<hash>`, parses its JSON stdout, and returns the started/reused
// container's id.
//
// --id-label serves double duty: it both sets the label on the container
// *and* is what the CLI queries against to decide whether an existing
// container can be reused. Passing both our repo-id and content-hash labels
// means the CLI's own idempotency check (verified in runtime-images.md's
// spike 1: ~1s on a warm call, same containerId) IS the cache: a matching
// container (same repo, same effective JSON) is reused untouched; a hash
// mismatch (config changed) means no container carries that label pair, so
// the CLI builds a fresh one — the old, now-unlabeled-as-current container is
// left for worktreesweep's reaping pass rather than removed here, since
// EnsureRunning's stale-container handling follows the same
// build-then-reap-later split.
func runDevcontainerUp(ctx context.Context, repoPath, configPath, repoID, hash string) (string, error) {
	out, err := exec.CommandContext(ctx, "devcontainer", "up",
		"--workspace-folder", repoPath,
		"--config", configPath,
		"--id-label", containerLabelRepo+"="+repoID,
		"--id-label", devcontainerLabel+"="+hash,
	).Output()
	if err != nil {
		msg := string(out)
		if ee, ok := err.(*exec.ExitError); ok {
			msg = string(ee.Stderr)
		}
		return "", fmt.Errorf("devcontainer up: %w (%s)", err, strings.TrimSpace(msg))
	}

	var res devcontainerUpResult
	if jerr := json.Unmarshal(out, &res); jerr != nil {
		return "", fmt.Errorf("parse devcontainer up output: %w", jerr)
	}
	if res.Outcome != "success" {
		return "", fmt.Errorf("devcontainer up outcome %q: %s", res.Outcome, res.Message)
	}
	if res.ContainerID == "" {
		return "", fmt.Errorf("devcontainer up succeeded but returned no containerId")
	}
	return res.ContainerID, nil
}

// devcontainerConfigDir is where this package writes the effective
// devcontainer.json it builds before invoking the CLI — a
// per-repo-deterministic temp path so concurrent EnsureDevcontainerRunning
// calls for different repos never collide, and re-running for the same repo
// simply overwrites the previous file (the CLI reads it fresh each call).
func devcontainerConfigPath(repoID string) string {
	return filepath.Join(os.TempDir(), "ate-devcontainer-"+repoID+".json")
}

// buildEffectiveDevcontainerJSON is the shared core of EnsureDevcontainerRunning
// and ExpectedDevcontainerHash: resolve the backend's own home dir (degrading
// to no credential mounts if unresolvable, same as EnsureRunning) and inject
// this RuntimeManager's mount/hardening contract into rawJSON.
func (m *RuntimeManager) buildEffectiveDevcontainerJSON(repoPath, rawJSON string) (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		homeDir = "" // see EnsureRunning's identical degrade-gracefully rationale
	}
	return BuildEffectiveDevcontainerJSON(rawJSON, injectedContract{
		RepoPath:      repoPath,
		MCPServerPath: m.MCPServerPath,
		HostHome:      homeDir,
	})
}

// ExpectedDevcontainerHash returns the devcontainerLabel hash a repo's
// container *should* currently carry, for worktreesweep's reaping pass to
// compare against what a live container actually has (mirroring how it
// already compares ate.image against repos.runtime_image). Returns "" for a
// repo with no devcontainer source configured (source == devcontainerNone),
// which the caller should treat as "no devcontainer container expected for
// this repo at all" rather than as a hash to match against.
func (m *RuntimeManager) ExpectedDevcontainerHash(repoPath, rawJSON string) (string, error) {
	if rawJSON == "" {
		return "", nil
	}
	effective, err := m.buildEffectiveDevcontainerJSON(repoPath, rawJSON)
	if err != nil {
		return "", err
	}
	return HashDevcontainerJSON(effective), nil
}

// EnsureDevcontainerRunning builds (or reuses) a repo's devcontainer-CLI
// container, returning its container id for spawn() (providers/cli.go) to
// `docker exec` into. rawJSON is the winning source's raw JSON, from
// ResolveDevcontainerSource; runtime_image must already be empty (checked by
// the caller, dispatcher.go — see runtime.go's EnsureRunning for the
// explicit-image path).
//
// Caching: computes the effective JSON, hashes it, and passes both the
// repo-id and content-hash as --id-label to `devcontainer up`. The CLI
// itself then does the cache check — a container already carrying both
// labels is reused as-is (verified idempotent and ~1s warm in
// runtime-images.md's spike 1); any other case (no container yet, or the
// hash changed) builds a fresh one. A now-stale container from a prior
// effective JSON is left running rather than removed here — worktreesweep's
// reaping pass (mirroring its existing ate.image handling) is what cleans it
// up, keeping this method's job to "ensure current config is running", not
// "garbage collect everything else".
func (m *RuntimeManager) EnsureDevcontainerRunning(ctx context.Context, repoID, repoPath, rawJSON string) (string, error) {
	lock := m.lockFor(repoID)
	lock.Lock()
	defer lock.Unlock()

	effective, err := m.buildEffectiveDevcontainerJSON(repoPath, rawJSON)
	if err != nil {
		return "", fmt.Errorf("devcontainer: build effective config: %w", err)
	}
	hash := HashDevcontainerJSON(effective)

	configPath := devcontainerConfigPath(repoID)
	if err := os.WriteFile(configPath, []byte(effective), 0o600); err != nil {
		return "", fmt.Errorf("devcontainer: write effective config: %w", err)
	}

	return runDevcontainerUp(ctx, repoPath, configPath, repoID, hash)
}
