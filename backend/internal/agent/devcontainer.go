package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"strings"
)

// SECURITY: this file generates the entire devcontainer.json from a fixed
// language allowlist plus this codebase's own mount/hardening contract —
// it never parses or merges a user-authored JSON blob. That is deliberate:
// an earlier version accepted a raw devcontainer.json from users, whose
// runArgs/mounts were merged (appended to) by our hardening flags, so a
// config containing e.g. "--privileged" survived and overrode
// --cap-drop=ALL regardless of ordering, converting "can PATCH a repo row"
// into host root (see commit 377b60f, which deleted that path entirely).
// The only user-controlled inputs here are a language id (checked against
// runtimeLanguageAllowlist — unknown id is an error) and a version string
// (checked against runtimeLanguageVersionRe — anything else is an error);
// neither ever becomes a Docker flag or mount, only a value inside a
// features/<ref> JSON object. If you are tempted to reintroduce a
// user-supplied devcontainer.json or free-form runArgs/mounts field, you
// must re-derive this analysis first.

// devcontainerLabel tags a devcontainer-CLI-managed runtime container with
// the sha256 of the generated devcontainer.json it was built from, the same
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

// devcontainerBaseImage is the base image used for a generated (language
// list) devcontainer.json. A plain Ubuntu base — the features below install
// the actual language toolchains on top of it.
const devcontainerBaseImage = "mcr.microsoft.com/devcontainers/base:ubuntu"

// claudeCodeFeatureRef installs the `claude` CLI into every generated runtime
// container. See GenerateDevcontainerJSON for why it is not optional.
const claudeCodeFeatureRef = "ghcr.io/anthropics/devcontainer-features/claude-code:1"

// runtimeLanguageAllowlist maps a user-selectable language id to its
// devcontainer feature reference. A table, not a plugin system — adding a
// language is one entry. This IS the injection boundary: an id not present
// here is rejected by ParseRuntimeLanguages before it ever reaches
// GenerateDevcontainerJSON, so no unlisted string can become a features key.
var runtimeLanguageAllowlist = map[string]string{
	"go":     "ghcr.io/devcontainers/features/go:1",
	"node":   "ghcr.io/devcontainers/features/node:2",
	"python": "ghcr.io/devcontainers/features/python:1",
	"rust":   "ghcr.io/devcontainers/features/rust:1",
	"java":   "ghcr.io/devcontainers/features/java:1",
	"ruby":   "ghcr.io/devcontainers/features/ruby:2",
}

// runtimeLanguageVersionRe bounds a language's version string: it becomes
// the "version" value inside a features/<ref> object, so it must never be
// able to break out of a JSON string value or carry shell/CLI metacharacters
// even though it's never passed to a shell — this is defense in depth, not
// the only control (json.Marshal already escapes it safely). Anchored,
// alphanumeric-plus-`._-`, max 32 chars.
var runtimeLanguageVersionRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

const runtimeLanguageVersionMaxLen = 32

// RuntimeLanguage is one user-selected language + version pair, validated by
// ParseRuntimeLanguages against runtimeLanguageAllowlist /
// runtimeLanguageVersionRe. ID and Version are the only user-authored
// strings that ever influence a generated devcontainer.json.
type RuntimeLanguage struct {
	ID      string `json:"id"`
	Version string `json:"version"`
}

// ParseRuntimeLanguages parses and validates repos.runtime_languages (a JSON
// array of {"id","version"} objects). Strict: an empty string means "no
// languages" (returns nil, nil), but any unknown id or invalid version is a
// hard error — never silently skipped or dropped, since a silently-dropped
// entry would mean the generated devcontainer.json doesn't match what a
// user or caller believes was configured.
func ParseRuntimeLanguages(raw string) ([]RuntimeLanguage, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	var langs []RuntimeLanguage
	if err := json.Unmarshal([]byte(raw), &langs); err != nil {
		return nil, fmt.Errorf("parse runtime_languages: %w", err)
	}
	// features is keyed by feature ref, so two entries with the same id
	// collapse to whichever came last — silently dropping a version the UI
	// still displays, which is exactly the "generated config doesn't match
	// what the caller believes was configured" failure this function's
	// contract rules out. Reject instead.
	seen := make(map[string]struct{}, len(langs))
	for _, l := range langs {
		if _, ok := runtimeLanguageAllowlist[l.ID]; !ok {
			return nil, fmt.Errorf("unknown runtime language id %q", l.ID)
		}
		if _, dup := seen[l.ID]; dup {
			return nil, fmt.Errorf("duplicate runtime language id %q", l.ID)
		}
		seen[l.ID] = struct{}{}
		if l.Version == "" {
			return nil, fmt.Errorf("runtime language %q: version is required", l.ID)
		}
		if len(l.Version) > runtimeLanguageVersionMaxLen {
			return nil, fmt.Errorf("runtime language %q: version %q exceeds max length %d", l.ID, l.Version, runtimeLanguageVersionMaxLen)
		}
		if !runtimeLanguageVersionRe.MatchString(l.Version) {
			return nil, fmt.Errorf("runtime language %q: version %q contains invalid characters", l.ID, l.Version)
		}
	}
	return langs, nil
}

// ReadRepoDevcontainerFile reads .devcontainer/devcontainer.json from a repo
// checkout, returning "" (no error) if the repo simply doesn't have one —
// that's the common case, not a failure. A real read error (permissions,
// I/O) is returned so the caller can decide how to treat it rather than
// silently falling through to the generated config.
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

// injectedContract is the mount/hardening contract every generated
// devcontainer build carries, regardless of which language list produced
// it — see GenerateDevcontainerJSON.
type injectedContract struct {
	RepoPath string
	// MCPServerPath is the mount target — the path the agent CLI inside the
	// container invokes the sidecar by. HostMCPBindSource is the source: its
	// location on the host, where the Docker daemon resolves bind sources.
	MCPServerPath     string
	HostMCPBindSource string
	// HostHome is the host-side home backing the credential mounts — the
	// bind *source*, resolved by the daemon on the host.
	HostHome string
	// LocalHome is where this process can stat those same dirs to decide
	// whether they exist. Equal to HostHome when the backend isn't
	// containerized; empty means "probe HostHome directly".
	LocalHome string
	// ExchangeDir carries the MCP config + RESULT_FILE handoff, bound at the
	// same path on both sides (providers.ExchangeDir). Empty omits it, which
	// costs the run every MCP tool.
	ExchangeDir string
}

// GenerateDevcontainerJSON builds a complete devcontainer.json from langs
// (already validated by ParseRuntimeLanguages) and this codebase's mount and
// hardening contract. This is generation, not a merge: every key is set
// here from trusted inputs (the fixed base image, the allowlisted feature
// refs, langs' validated id/version pairs, and contract's paths) — there is
// no step that parses or folds in a user-authored JSON document, so there is
// no way for a user-controlled string to reach runArgs or mounts. See the
// SECURITY comment at the top of this file.
//
// Contract injected (mirrors runtime.go's buildDockerRunArgs for an
// explicit-runtime_image container):
//   - workspaceMount + workspaceFolder pinned to the same absolute
//     repoPath on both sides — a git worktree's `.git` is a file holding an
//     absolute gitdir pointer, so the repo must land at the exact same path
//     inside the container as on the host.
//   - /tmp bound at /tmp (MCP config + RESULT_FILE handoff, see
//     providers/mcp.go).
//   - the MCP sidecar binary, read-only, at its own absolute path (the
//     generated MCP config JSON references it by absolute path).
//   - each of credentialDirs that exists on the host, read-write, from
//     HostHome/<dir> to RuntimeContainerHome/<dir>. Gated on os.Stat like
//     buildDockerRunArgs: `docker run` refuses a bind mount whose source is
//     missing, so listing all four breaks every deployment that doesn't use
//     every provider. The resulting host-dependence is handled at the other
//     end — HashDevcontainerJSON strips these entries before hashing, so the
//     cache key stays a pure function of the language list (see
//     hashableDevcontainerJSON).
//   - runArgs carrying this codebase's hardening flags
//     (--security-opt=no-new-privileges, --cap-drop=ALL) — the only
//     runArgs/mounts entries that exist, since nothing is merged in.
//
// Deterministic key order (via marshalSorted) so HashDevcontainerJSON is a
// stable cache key across process restarts for the same language list.
func GenerateDevcontainerJSON(langs []RuntimeLanguage, contract injectedContract) (string, error) {
	// The agent CLI has to exist inside the container we're about to exec
	// into: providers/cli.go's spawn() runs `docker exec <container> claude
	// …`, resolved on that container's PATH. The devcontainer base image has
	// no CLI, so without this a run gets the repo's toolchain and then
	// immediately fails to start the agent.
	//
	// Pinned to the feature's major (`:1`) like the language features above.
	// Note that pins the installer, not the CLI it fetches — the version
	// inside a runtime container can drift from CLAUDE_CLI_VERSION in
	// backend/Dockerfile, which governs in-process runs. Acceptable while
	// this is opt-in per repo; if the drift matters, publish a base image
	// with the CLI baked in and generate against that instead.
	features := map[string]any{
		claudeCodeFeatureRef: map[string]any{},
	}
	for _, l := range langs {
		ref, ok := runtimeLanguageAllowlist[l.ID]
		if !ok {
			// ParseRuntimeLanguages should already have rejected this —
			// defense in depth, not reachable via the normal call path.
			return "", fmt.Errorf("unknown runtime language id %q", l.ID)
		}
		features[ref] = map[string]any{"version": l.Version}
	}

	mounts := []string{}
	if contract.ExchangeDir != "" {
		// Same path on both sides: the sidecar is launched inside the
		// container with a config path this process wrote, and this process
		// reads back the RESULT_FILE the sidecar wrote. Both only work if the
		// directory resolves identically on the host and in the container.
		mounts = append(mounts, fmt.Sprintf(
			"source=%s,target=%s,type=bind", contract.ExchangeDir, contract.ExchangeDir,
		))
	}
	if contract.MCPServerPath != "" && contract.HostMCPBindSource != "" {
		mounts = append(mounts, fmt.Sprintf(
			"source=%s,target=%s,type=bind,readonly", contract.HostMCPBindSource, contract.MCPServerPath,
		))
	}
	if contract.HostHome != "" {
		for _, dir := range credentialDirs {
			src := filepath.Join(contract.HostHome, dir)
			// Existence is checked against LocalHome — the path this process
			// can actually stat — while src (the host path) is what the
			// daemon resolves. When the backend is containerized these
			// differ: compose mounts ${HOME}/.claude at /home/node/.claude,
			// so statting the host path from in here always fails and would
			// silently drop every credential mount.
			probe := src
			if contract.LocalHome != "" {
				probe = filepath.Join(contract.LocalHome, dir)
			}
			// Only mount what exists. `docker run` refuses a bind whose
			// source is missing outright ("bind source path does not
			// exist") — it auto-creates for volumes, not binds — so an
			// unconditional list fails the whole build on any deployment
			// that doesn't use every provider. See hashableDevcontainerJSON
			// for how the cache key stays stable despite this being
			// host-dependent.
			if _, err := os.Stat(probe); err != nil {
				continue
			}
			dst := RuntimeContainerHome + "/" + dir
			mounts = append(mounts, fmt.Sprintf("source=%s,target=%s,type=bind", src, dst))
		}
	}

	cfg := map[string]any{
		"image":           devcontainerBaseImage,
		"features":        features,
		"mounts":          mounts,
		"runArgs":         []string{"--security-opt=no-new-privileges", "--cap-drop=ALL"},
		"workspaceMount":  fmt.Sprintf("source=%s,target=%s,type=bind", contract.RepoPath, contract.RepoPath),
		"workspaceFolder": contract.RepoPath,
	}

	out, err := marshalSorted(cfg)
	if err != nil {
		return "", fmt.Errorf("marshal generated devcontainer.json: %w", err)
	}
	return out, nil
}

// marshalSorted JSON-encodes v with map keys in sorted order, so identical
// logical content always produces byte-identical output — required for
// HashDevcontainerJSON to be a stable cache key across process restarts
// (Go's encoding/json already sorts map[string]any keys on marshal, but this
// helper makes that guarantee explicit rather than relying on stdlib
// implementation detail without a test locking it in).
func marshalSorted(cfg map[string]any) (string, error) {
	// encoding/json marshals map[string]any with its keys sorted, so a plain
	// Marshal is already deterministic — the guarantee comes from the stdlib,
	// not from anything this function does. TestHashDevcontainerJSON_SameInputSameHash
	// is what actually locks it in; if that ever breaks, marshal an explicit
	// sorted key list here rather than reintroducing a sort whose result is
	// discarded.
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
	sum := sha256.Sum256([]byte(hashableDevcontainerJSON(effectiveJSON)))
	return hex.EncodeToString(sum[:])
}

// hashableDevcontainerJSON strips credential mounts before hashing.
//
// Those mounts are necessarily host-dependent — a bind whose source doesn't
// exist fails `docker run`, so the mount list can only contain the credential
// dirs actually present. But that makes the raw JSON a function of host state
// as well as configuration: creating ~/.codex (say, by running a codex chat
// once) would change the hash and force a full rebuild of a container whose
// configuration nobody touched, and could leave the sweeper and the
// dispatcher computing different hashes for the same repo at the same moment.
//
// Hashing the config with those entries removed keeps the cache key a pure
// function of the language list and repo path, while the built container
// still gets whatever credentials exist. A container built before a provider
// was configured keeps running without that provider's mount until something
// else invalidates it — the acceptable half of this trade, and the reason
// this strips rather than reordering the two concerns.
func hashableDevcontainerJSON(effectiveJSON string) string {
	var cfg map[string]any
	if err := json.Unmarshal([]byte(effectiveJSON), &cfg); err != nil {
		return effectiveJSON // not ours to interpret (repo-committed file) — hash verbatim
	}
	mounts, ok := cfg["mounts"].([]any)
	if !ok {
		return effectiveJSON
	}
	kept := make([]any, 0, len(mounts))
	for _, m := range mounts {
		s, _ := m.(string)
		if strings.Contains(s, ",target="+RuntimeContainerHome+"/") {
			continue // a credential mount; presence varies by host
		}
		kept = append(kept, m)
	}
	cfg["mounts"] = kept
	out, err := marshalSorted(cfg)
	if err != nil {
		return effectiveJSON
	}
	return out
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
// container (same repo, same generated JSON) is reused untouched; a hash
// mismatch (config changed) means no container carries that label pair, so
// the CLI builds a fresh one — the old, now-unlabeled-as-current container is
// left for worktreesweep's reaping pass rather than removed here, since
// EnsureRunning's stale-container handling follows the same
// build-then-reap-later split.
// devcontainerEnv is the environment for the `devcontainer` CLI subprocess.
//
// It cannot simply inherit the backend's: entrypoint.sh drops root to `node`
// via su-exec, which does not reset HOME, so the server process runs as uid
// 1000 while HOME still points at /root. The CLI shells out to `docker`,
// which then tries to read /root/.docker/config.json, gets EACCES, and prints
// a warning to stderr — and the CLI treats *any* stderr from its container
// lookup as a failed command:
//
//	Error: Command failed: docker ps -q -a --filter label=ate.repo_id=...
//
// with no underlying error to explain it. Point HOME at the account the
// process actually runs as so docker reads a config it can open.
//
// PATH is passed through because the CLI resolves `docker` by name.
func devcontainerEnv() []string {
	home := "/home/node"
	if u, err := user.Current(); err == nil && u.HomeDir != "" {
		home = u.HomeDir
	}
	env := []string{"HOME=" + home}
	if p := os.Getenv("PATH"); p != "" {
		env = append(env, "PATH="+p)
	}
	// The CLI is Node; keep the proxy/TLS vars an operator may have set for a
	// corporate network (see SSL_CA_CERT_PATH in AGENTS.md).
	for _, k := range []string{
		"NODE_EXTRA_CA_CERTS", "NODE_TLS_REJECT_UNAUTHORIZED",
		"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY",
		"http_proxy", "https_proxy", "no_proxy",
		"DOCKER_HOST", "DOCKER_CONFIG",
	} {
		if v := os.Getenv(k); v != "" {
			env = append(env, k+"="+v)
		}
	}
	return env
}

func runDevcontainerUp(ctx context.Context, repoPath, configPath, repoID, hash string) (string, error) {
	cmd := exec.CommandContext(ctx, "devcontainer", "up",
		"--workspace-folder", repoPath,
		"--config", configPath,
		"--id-label", containerLabelRepo+"="+repoID,
		"--id-label", devcontainerLabel+"="+hash,
	)
	cmd.Env = devcontainerEnv()
	out, err := cmd.Output()
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

// devcontainerConfigPath is where this package writes the generated
// devcontainer.json it builds before invoking the CLI — a
// per-repo-deterministic temp path so concurrent EnsureDevcontainerRunning
// calls for different repos never collide, and re-running for the same repo
// simply overwrites the previous file (the CLI reads it fresh each call).
func devcontainerConfigPath(repoID string) string {
	// The CLI validates the config's *basename*, not just its contents:
	// anything other than "devcontainer.json" or ".devcontainer.json" is
	// rejected outright ("Filename must be devcontainer.json or
	// .devcontainer.json"). So the per-repo uniqueness has to live in a
	// containing directory rather than in the filename — an
	// "ate-devcontainer-<id>.json" temp file parses fine and still fails.
	return filepath.Join(os.TempDir(), "ate-devcontainer-"+repoID, "devcontainer.json")
}

// buildGeneratedDevcontainerJSON is the shared core of
// EnsureDevcontainerRunning and ExpectedDevcontainerHash: resolve the
// backend's own home dir (degrading to no credential mounts if
// unresolvable, same as EnsureRunning) and generate this RuntimeManager's
// mount/hardening contract into a devcontainer.json for langs.
func (m *RuntimeManager) buildGeneratedDevcontainerJSON(repoPath string, langs []RuntimeLanguage) (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		homeDir = "" // see EnsureRunning's identical degrade-gracefully rationale
	}
	return GenerateDevcontainerJSON(langs, injectedContract{
		RepoPath:          repoPath,
		MCPServerPath:     m.MCPServerPath,
		HostMCPBindSource: m.bindMCPServerPath(),
		HostHome:          m.bindHome(homeDir),
		LocalHome:         homeDir,
		ExchangeDir:       m.ExchangeDir,
	})
}

// ExpectedDevcontainerHash returns the devcontainerLabel hash a repo's
// container *should* currently carry, for worktreesweep's reaping pass to
// compare against what a live container actually has (mirroring how it
// already compares ate.image against repos.runtime_image). Returns "" for a
// repo with no languages configured, which the caller should treat as "no
// devcontainer container expected for this repo at all" rather than as a
// hash to match against.
func (m *RuntimeManager) ExpectedDevcontainerHash(repoPath string, langs []RuntimeLanguage) (string, error) {
	if len(langs) == 0 {
		return "", nil
	}
	generated, err := m.buildGeneratedDevcontainerJSON(repoPath, langs)
	if err != nil {
		return "", err
	}
	return HashDevcontainerJSON(generated), nil
}

// GeneratedDevcontainerJSON exposes buildGeneratedDevcontainerJSON for
// callers outside this package (the /repos/{id}/devcontainer handler) that
// need the actual effective_json, not just its hash — reusing the same
// resolution/generation logic EnsureDevcontainerRunning and
// ExpectedDevcontainerHash already use rather than re-deriving it. Returns
// "" for an empty langs, same "nothing configured" convention as
// ExpectedDevcontainerHash.
func (m *RuntimeManager) GeneratedDevcontainerJSON(repoPath string, langs []RuntimeLanguage) (string, error) {
	if len(langs) == 0 {
		return "", nil
	}
	return m.buildGeneratedDevcontainerJSON(repoPath, langs)
}

// ExpectedDevcontainerHashFromFile is ExpectedDevcontainerHash's sibling for
// a repo-committed devcontainer.json (resolution order step 2): the hash of
// rawJSON, completely unmodified — see EnsureDevcontainerRunningFromFile.
// Returns "" for an empty rawJSON (no repo-committed file), same
// "no container expected" convention as ExpectedDevcontainerHash.
func (m *RuntimeManager) ExpectedDevcontainerHashFromFile(rawJSON string) string {
	if rawJSON == "" {
		return ""
	}
	return HashDevcontainerJSON(rawJSON)
}

// EnsureDevcontainerRunning builds (or reuses) a repo's devcontainer-CLI
// container from a generated devcontainer.json, returning its container id
// for spawn() (providers/cli.go) to `docker exec` into. langs is the
// caller's already-resolved language list (dispatcher.go calls this only
// once it knows runtime_image is empty and there is no repo-committed
// devcontainer.json — see the resolution order in docs/runtime-containers.md).
//
// Caching: computes the generated JSON, hashes it, and passes both the
// repo-id and content-hash as --id-label to `devcontainer up`. The CLI
// itself then does the cache check — a container already carrying both
// labels is reused as-is (verified idempotent and ~1s warm in
// runtime-images.md's spike 1); any other case (no container yet, or the
// hash changed) builds a fresh one. A now-stale container from a prior
// generated JSON is left running rather than removed here — worktreesweep's
// reaping pass (mirroring its existing ate.image handling) is what cleans it
// up, keeping this method's job to "ensure current config is running", not
// "garbage collect everything else".
func (m *RuntimeManager) EnsureDevcontainerRunning(ctx context.Context, repoID, repoPath string, langs []RuntimeLanguage) (string, error) {
	lock := m.lockFor(repoID)
	lock.Lock()
	defer lock.Unlock()

	generated, err := m.buildGeneratedDevcontainerJSON(repoPath, langs)
	if err != nil {
		return "", fmt.Errorf("devcontainer: build generated config: %w", err)
	}
	hash := HashDevcontainerJSON(generated)

	configPath := devcontainerConfigPath(repoID)
	if err := os.MkdirAll(filepath.Dir(configPath), 0o700); err != nil {
		return "", fmt.Errorf("create devcontainer config dir: %w", err)
	}
	if err := os.WriteFile(configPath, []byte(generated), 0o600); err != nil {
		return "", fmt.Errorf("devcontainer: write generated config: %w", err)
	}

	return runDevcontainerUp(ctx, repoPath, configPath, repoID, hash)
}

// EnsureDevcontainerRunningFromFile is EnsureDevcontainerRunning's sibling
// for resolution order step 2: a repo-committed .devcontainer/devcontainer.json
// (rawJSON, from ReadRepoDevcontainerFile). Repo-committed content is code
// the agent already runs against — not a new trust boundary the way a
// UI-authored blob would be (see the SECURITY comment at the top of this
// file) — but it is still never merged with the generated mount/hardening
// contract: this function passes rawJSON to `devcontainer up` completely
// unmodified. A repo whose committed devcontainer.json needs this codebase's
// mounts (MCP sidecar, credential dirs) must declare them itself; there is
// no injection step here to add them silently.
func (m *RuntimeManager) EnsureDevcontainerRunningFromFile(ctx context.Context, repoID, repoPath, rawJSON string) (string, error) {
	lock := m.lockFor(repoID)
	lock.Lock()
	defer lock.Unlock()

	hash := HashDevcontainerJSON(rawJSON)

	configPath := devcontainerConfigPath(repoID)
	if err := os.MkdirAll(filepath.Dir(configPath), 0o700); err != nil {
		return "", fmt.Errorf("create devcontainer config dir: %w", err)
	}
	if err := os.WriteFile(configPath, []byte(rawJSON), 0o600); err != nil {
		return "", fmt.Errorf("devcontainer: write repo-file config: %w", err)
	}

	return runDevcontainerUp(ctx, repoPath, configPath, repoID, hash)
}
