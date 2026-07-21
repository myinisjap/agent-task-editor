package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/myinisjap/agent-task-editor/backend/internal/api/middleware"
	"github.com/myinisjap/agent-task-editor/backend/internal/ghclient"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

// validGitRef matches HEAD, HEAD~N, HEAD^N, 40/64-char hex SHAs, and safe branch/tag names.
// First char must be alphanumeric to prevent flag injection (e.g. --no-index).
var validGitRef = regexp.MustCompile(`^(HEAD([~^][0-9]+)?|[0-9a-f]{40,64}|[a-zA-Z0-9][a-zA-Z0-9._/-]*)$`)

func isValidGitRef(ref string) bool {
	return validGitRef.MatchString(ref) && !strings.Contains(ref, "..")
}

// RepoEventPublisher publishes repo lifecycle events (e.g. async clone
// completion) to connected WebSocket clients. Satisfied by *ws.Hub.
type RepoEventPublisher interface {
	Publish(eventType string, payload map[string]any)
}

type ReposHandler struct {
	q           *gen.Queries
	repoBaseDir string // host-side base dir; paths under it are rewritten to /repos inside the container
	pub         RepoEventPublisher
}

func NewReposHandler(q *gen.Queries, repoBaseDir string, pub RepoEventPublisher) *ReposHandler {
	return &ReposHandler{q: q, repoBaseDir: repoBaseDir, pub: pub}
}

func (h *ReposHandler) List(w http.ResponseWriter, r *http.Request) {
	repos, err := h.q.ListRepos(r.Context())
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	JSON(w, http.StatusOK, repos)
}

func (h *ReposHandler) Get(w http.ResponseWriter, r *http.Request) {
	repo, err := h.q.GetRepo(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		Err(w, http.StatusNotFound, "repo not found")
		return
	}
	JSON(w, http.StatusOK, repo)
}

// createRepoBody is the decoded request payload for ReposHandler.Create.
type createRepoBody struct {
	Name                  string  `json:"name"`
	Path                  string  `json:"path"`
	RemoteURL             *string `json:"remote_url"`
	WorkflowID            *string `json:"workflow_id"`
	IssueSyncEnabled      bool    `json:"issue_sync_enabled"`
	IssueSyncLabel        string  `json:"issue_sync_label"`
	IssueWritebackEnabled bool    `json:"issue_writeback_enabled"`
}

func (h *ReposHandler) Create(w http.ResponseWriter, r *http.Request) {
	var body createRepoBody
	if err := decode(r, &body); err != nil {
		Err(w, http.StatusBadRequest, "invalid request body")
		return
	}

	remoteURL := ""
	if body.RemoteURL != nil {
		remoteURL = strings.TrimSpace(*body.RemoteURL)
	}

	// Expand ~ to the home directory so users can type ~/foo paths.
	if strings.HasPrefix(body.Path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			body.Path = filepath.Join(home, body.Path[2:])
		}
	}

	// Auto-derive name from remote URL if not provided.
	if body.Name == "" && remoteURL != "" {
		if derived, ok := ghclient.ParseGitHubName(remoteURL); ok {
			body.Name = derived
		}
	}

	// Auto-clone when no local path is provided. The git clone itself runs
	// asynchronously (see cloneRepoAsync below); here we only validate inputs and
	// derive the destination path.
	isClone := body.Path == ""
	if isClone {
		destPath, ok := h.resolveCloneDestination(w, remoteURL, body.Name)
		if !ok {
			return
		}
		body.Path = destPath
	}

	// name must be known by now.
	if body.Name == "" {
		Err(w, http.StatusBadRequest, "name is required (or provide a GitHub remote_url for auto-detection)")
		return
	}

	if !isClone && !h.validateExistingRepoPath(w, r, body.Path) {
		return
	}

	issueSyncEnabled, issueWritebackEnabled, ok := resolveIssueFlags(w, &body, remoteURL)
	if !ok {
		return
	}

	repo, err := h.q.CreateRepo(r.Context(), gen.CreateRepoParams{
		ID:                    uuid.NewString(),
		Name:                  body.Name,
		Path:                  body.Path,
		RemoteUrl:             body.RemoteURL,
		WorkflowID:            body.WorkflowID,
		IssueSyncEnabled:      issueSyncEnabled,
		IssueSyncLabel:        strings.TrimSpace(body.IssueSyncLabel),
		IssueWritebackEnabled: issueWritebackEnabled,
	})
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}

	if isClone {
		h.startAsyncClone(r, &repo, remoteURL, body.Path)
	} else {
		setClaudeTrust(r.Context(), body.Path)
	}
	JSON(w, http.StatusCreated, repo)
}

// resolveCloneDestination validates the auto-clone inputs and derives the clone
// destination path under repoBaseDir. It writes the 400 response and returns
// ok=false on the first violation.
func (h *ReposHandler) resolveCloneDestination(w http.ResponseWriter, remoteURL, name string) (destPath string, ok bool) {
	if remoteURL == "" {
		Err(w, http.StatusBadRequest, "path or remote_url is required")
		return "", false
	}
	if h.repoBaseDir == "" {
		Err(w, http.StatusBadRequest, "repo_base_dir must be configured on the server to enable auto-cloning")
		return "", false
	}

	// Derive the clone destination from the parsed name (org/repo) or fall back
	// to the last path segment of the remote URL.
	cloneSubdir := name
	if cloneSubdir == "" {
		// Name not yet known; use last segment of URL as subdir.
		seg := remoteURL[strings.LastIndex(remoteURL, "/")+1:]
		cloneSubdir = strings.TrimSuffix(seg, ".git")
	}
	destPath = filepath.Join(h.repoBaseDir, cloneSubdir)

	// Validate destPath is within repoBaseDir BEFORE any filesystem operations
	// to prevent path traversal via a crafted name or URL segment (e.g. "../../etc").
	// The destination doesn't exist yet, so this is a lexical (cleaned-path)
	// check; withinBaseDir's symlink resolution is for already-existing paths.
	cleanDest := filepath.Clean(destPath)
	cleanBase := filepath.Clean(h.repoBaseDir)
	sep := string(os.PathSeparator)
	if cleanDest != cleanBase && !strings.HasPrefix(cleanDest+sep, cleanBase+sep) {
		Err(w, http.StatusBadRequest, "derived clone path is outside the allowed base directory")
		return "", false
	}

	// Only allow https:// and git@ schemes to avoid unexpected behaviour.
	if !strings.HasPrefix(remoteURL, "https://") && !strings.HasPrefix(remoteURL, "git@") {
		Err(w, http.StatusBadRequest, "remote_url must use https:// or git@ scheme")
		return "", false
	}

	// Refuse to clone over an existing directory — the async clone would fail
	// anyway, and this keeps the error synchronous and clear.
	if _, err := os.Stat(destPath); err == nil {
		Err(w, http.StatusBadRequest, "clone destination already exists")
		return "", false
	}

	return destPath, true
}

// validateExistingRepoPath enforces the base-dir restriction (resolving symlinks,
// consistent with Update) and verifies path is a git repository before persisting.
// It writes the 400 response and returns false on failure. Only used for the
// existing-local-path branch; the clone path skips both checks since the
// directory doesn't exist yet.
func (h *ReposHandler) validateExistingRepoPath(w http.ResponseWriter, r *http.Request, path string) bool {
	if h.repoBaseDir != "" && !withinBaseDir(path, h.repoBaseDir) {
		Err(w, http.StatusBadRequest, "repo path is outside the allowed base directory")
		return false
	}
	if err := exec.CommandContext(r.Context(), "git", "-C", path, "rev-parse", "--git-dir").Run(); err != nil {
		Err(w, http.StatusBadRequest, "path is not a git repository")
		return false
	}
	return true
}

// resolveIssueFlags validates and resolves the issue-sync and issue-writeback
// flags to their 0/1 column values. It writes the 400 response and returns
// ok=false on the first violation.
func resolveIssueFlags(w http.ResponseWriter, body *createRepoBody, remoteURL string) (issueSyncEnabled, issueWritebackEnabled int64, ok bool) {
	if body.IssueSyncEnabled {
		if remoteURL == "" {
			Err(w, http.StatusBadRequest, "issue sync requires a GitHub remote_url")
			return 0, 0, false
		}
		if body.WorkflowID == nil || *body.WorkflowID == "" {
			Err(w, http.StatusBadRequest, "issue sync requires a workflow (imported issues become tasks in that workflow)")
			return 0, 0, false
		}
		issueSyncEnabled = 1
	}
	if body.IssueWritebackEnabled {
		if remoteURL == "" {
			Err(w, http.StatusBadRequest, "issue write-back requires a GitHub remote_url")
			return 0, 0, false
		}
		issueWritebackEnabled = 1
	}
	return issueSyncEnabled, issueWritebackEnabled, true
}

// startAsyncClone marks the freshly-created repo row 'cloning' and kicks off the
// background clone. Runs in the background so a slow clone of a large repo doesn't
// exceed the server's WriteTimeout and get cut off mid-clone. The UI shows a
// spinner and refreshes on the repo.clone_done / repo.clone_failed WS event.
func (h *ReposHandler) startAsyncClone(r *http.Request, repo *gen.Repo, remoteURL, destPath string) {
	if err := h.q.SetRepoCloneStatus(r.Context(), gen.SetRepoCloneStatusParams{
		CloneStatus: "cloning",
		CloneError:  "",
		ID:          repo.ID,
	}); err != nil {
		middleware.LoggerFromContext(r.Context()).Warn("failed to mark repo cloning", "repo_id", repo.ID, "err", err)
	}
	repo.CloneStatus = "cloning"
	go h.cloneRepoAsync(repo.ID, remoteURL, destPath)
}

// cloneRepoAsync performs the git clone for an auto-cloned repo outside the HTTP
// request. On success it marks the repo 'ready' and records claude trust; on
// failure it records the error, removes the partial clone directory (so a retry
// can reuse the path), and marks the repo 'error'. Either way it publishes a WS
// event so connected boards refresh.
func (h *ReposHandler) cloneRepoAsync(repoID, remoteURL, destPath string) {
	// Detached from any request context; a generous ceiling guards against a
	// clone that hangs indefinitely.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	fail := func(msg string) {
		_ = os.RemoveAll(destPath) // best-effort partial-clone cleanup
		if err := h.q.SetRepoCloneStatus(ctx, gen.SetRepoCloneStatusParams{
			CloneStatus: "error",
			CloneError:  msg,
			ID:          repoID,
		}); err != nil {
			slog.Warn("cloneRepoAsync: mark error", "repo_id", repoID, "err", err)
		}
		h.publishRepoEvent("repo.clone_failed", repoID, "error", msg)
	}

	// Create parent directory structure (e.g. repoBaseDir/org/).
	if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
		fail(fmt.Sprintf("failed to create parent directory: %v", err))
		return
	}
	if out, err := exec.CommandContext(ctx, "git", "clone", remoteURL, destPath).CombinedOutput(); err != nil {
		fail(fmt.Sprintf("git clone failed: %s", strings.TrimSpace(string(out))))
		return
	}

	if err := h.q.SetRepoCloneStatus(ctx, gen.SetRepoCloneStatusParams{
		CloneStatus: "ready",
		CloneError:  "",
		ID:          repoID,
	}); err != nil {
		slog.Warn("cloneRepoAsync: mark ready", "repo_id", repoID, "err", err)
	}
	setClaudeTrust(ctx, destPath)
	h.publishRepoEvent("repo.clone_done", repoID, "ready", "")
}

// publishRepoEvent broadcasts a repo clone lifecycle event to WS clients.
func (h *ReposHandler) publishRepoEvent(event, repoID, status, errMsg string) {
	if h.pub == nil {
		return
	}
	h.pub.Publish(event, map[string]any{
		"repo_id":      repoID,
		"clone_status": status,
		"clone_error":  errMsg,
	})
}

// withinBaseDir reports whether path is base itself or nested under it, after
// resolving symlinks on both (falling back to a lexical clean when a path can't
// be resolved — e.g. it doesn't exist yet). Shared by Create and Update so the
// two agree on containment: a symlink under the base pointing outside it is
// rejected by both, not just Update.
func withinBaseDir(path, base string) bool {
	realPath, err := filepath.EvalSymlinks(path)
	if err != nil {
		realPath = filepath.Clean(path)
	}
	realBase, err := filepath.EvalSymlinks(base)
	if err != nil {
		realBase = filepath.Clean(base)
	}
	sep := string(os.PathSeparator)
	return realPath == realBase || strings.HasPrefix(realPath+sep, realBase+sep)
}

func (h *ReposHandler) Update(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	// Fetch existing repo so we can fall back to its values for omitted fields.
	existing, err := h.q.GetRepo(r.Context(), id)
	if err != nil {
		Err(w, http.StatusNotFound, "repo not found")
		return
	}

	var body struct {
		Name                  *string `json:"name"`
		Path                  *string `json:"path"`
		RemoteURL             *string `json:"remote_url"`
		WorkflowID            *string `json:"workflow_id"`
		IssueSyncEnabled      *bool   `json:"issue_sync_enabled"`
		IssueSyncLabel        *string `json:"issue_sync_label"`
		IssueWritebackEnabled *bool   `json:"issue_writeback_enabled"`
	}
	if err := decode(r, &body); err != nil {
		Err(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// Merge: use provided value or fall back to existing.
	name := existing.Name
	if body.Name != nil {
		name = strings.TrimSpace(*body.Name)
	}

	path := existing.Path
	if body.Path != nil {
		path = strings.TrimSpace(*body.Path)
	}

	remoteURL := existing.RemoteUrl
	if body.RemoteURL != nil {
		trimmed := strings.TrimSpace(*body.RemoteURL)
		if trimmed == "" {
			remoteURL = nil
		} else {
			remoteURL = &trimmed
		}
	}

	workflowID := existing.WorkflowID
	if body.WorkflowID != nil {
		trimmed := strings.TrimSpace(*body.WorkflowID)
		if trimmed == "" {
			workflowID = nil
		} else {
			workflowID = &trimmed
		}
	}

	issueSyncEnabled := existing.IssueSyncEnabled
	if body.IssueSyncEnabled != nil {
		issueSyncEnabled = 0
		if *body.IssueSyncEnabled {
			issueSyncEnabled = 1
		}
	}

	issueSyncLabel := existing.IssueSyncLabel
	if body.IssueSyncLabel != nil {
		issueSyncLabel = strings.TrimSpace(*body.IssueSyncLabel)
	}

	issueWritebackEnabled := existing.IssueWritebackEnabled
	if body.IssueWritebackEnabled != nil {
		issueWritebackEnabled = 0
		if *body.IssueWritebackEnabled {
			issueWritebackEnabled = 1
		}
	}

	if issueSyncEnabled != 0 {
		if remoteURL == nil || *remoteURL == "" {
			Err(w, http.StatusBadRequest, "issue sync requires a GitHub remote_url")
			return
		}
		if workflowID == nil || *workflowID == "" {
			Err(w, http.StatusBadRequest, "issue sync requires a workflow (imported issues become tasks in that workflow)")
			return
		}
	}

	if issueWritebackEnabled != 0 {
		if remoteURL == nil || *remoteURL == "" {
			Err(w, http.StatusBadRequest, "issue write-back requires a GitHub remote_url")
			return
		}
	}

	// Validate required fields.
	if name == "" {
		Err(w, http.StatusBadRequest, "name is required")
		return
	}
	if path == "" {
		Err(w, http.StatusBadRequest, "path is required")
		return
	}

	// Expand ~ to the home directory.
	if strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			path = filepath.Join(home, path[2:])
		}
	}

	// Enforce base-dir restriction when configured.
	if h.repoBaseDir != "" && !withinBaseDir(path, h.repoBaseDir) {
		Err(w, http.StatusBadRequest, "repo path is outside the allowed base directory")
		return
	}

	// Verify the path is still a valid git repository.
	if err := exec.CommandContext(r.Context(), "git", "-C", path, "rev-parse", "--git-dir").Run(); err != nil {
		Err(w, http.StatusBadRequest, "path is not a git repository")
		return
	}

	repo, err := h.q.UpdateRepo(r.Context(), gen.UpdateRepoParams{
		ID:                    id,
		Name:                  name,
		Path:                  path,
		RemoteUrl:             remoteURL,
		WorkflowID:            workflowID,
		IssueSyncEnabled:      issueSyncEnabled,
		IssueSyncLabel:        issueSyncLabel,
		IssueWritebackEnabled: issueWritebackEnabled,
	})
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	setClaudeTrust(r.Context(), path)
	JSON(w, http.StatusOK, repo)
}

func (h *ReposHandler) Delete(w http.ResponseWriter, r *http.Request) {
	if err := h.q.DeleteRepo(r.Context(), chi.URLParam(r, "id")); err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *ReposHandler) Tree(w http.ResponseWriter, r *http.Request) {
	repo, err := h.q.GetRepo(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		Err(w, http.StatusNotFound, "repo not found")
		return
	}

	ref := r.URL.Query().Get("ref")
	if ref == "" {
		ref = "HEAD"
	}
	if !isValidGitRef(ref) {
		Err(w, http.StatusBadRequest, "invalid git ref")
		return
	}

	out, err := exec.CommandContext(r.Context(), "git", "-C", repo.Path, "ls-tree", "-r", "--name-only", "--", ref).Output()
	if err != nil {
		Err(w, http.StatusInternalServerError, "failed to read git tree")
		return
	}

	files := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(files) == 1 && files[0] == "" {
		files = []string{}
	}
	JSON(w, http.StatusOK, map[string]any{"ref": ref, "files": files})
}

// setClaudeTrust marks the given repo path as trust-dialog-accepted in ~/.claude.json
// so headless Claude Code agents can use pre-approved permissions without prompting.
func setClaudeTrust(ctx context.Context, repoPath string) {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	claudeJSON := filepath.Join(home, ".claude.json")

	data, err := os.ReadFile(claudeJSON)
	if err != nil {
		return
	}

	var cfg map[string]any
	if err := json.Unmarshal(data, &cfg); err != nil {
		return
	}

	projects, _ := cfg["projects"].(map[string]any)
	if projects == nil {
		projects = map[string]any{}
		cfg["projects"] = projects
	}

	entry, _ := projects[repoPath].(map[string]any)
	if entry == nil {
		entry = map[string]any{}
		projects[repoPath] = entry
	}
	entry["hasTrustDialogAccepted"] = true

	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return
	}

	// Write atomically: a concurrent claude CLI subprocess (an agent run) may be
	// reading or rewriting this same file. Write to a temp file in the same
	// directory and rename over the original — os.Rename is atomic on the same
	// filesystem, so readers see either the old or the new file, never a
	// half-written one. A crash mid-write leaves only an orphaned temp file.
	if err := atomicWriteFile(claudeJSON, out, 0o600); err != nil {
		middleware.LoggerFromContext(ctx).Warn("failed to update claude trust dialog", "path", repoPath, "err", err)
	}
}

// atomicWriteFile writes data to path atomically by creating a temp file in the
// same directory, writing and syncing it, then renaming it over path. The temp
// file is removed on any error before the rename. The final file has the given
// mode.
func atomicWriteFile(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	// Best-effort cleanup if we don't make it to a successful rename.
	defer func() { _ = os.Remove(tmpName) }()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, mode); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
