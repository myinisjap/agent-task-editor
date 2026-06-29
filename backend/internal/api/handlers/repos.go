package handlers

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/myinisjap/agent-task-editor/backend/internal/ghclient"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

// validGitRef matches HEAD, HEAD~N, HEAD^N, 40/64-char hex SHAs, and safe branch/tag names.
// First char must be alphanumeric to prevent flag injection (e.g. --no-index).
var validGitRef = regexp.MustCompile(`^(HEAD([~^][0-9]+)?|[0-9a-f]{40,64}|[a-zA-Z0-9][a-zA-Z0-9._/-]*)$`)

func isValidGitRef(ref string) bool {
	return validGitRef.MatchString(ref) && !strings.Contains(ref, "..")
}

type ReposHandler struct {
	q           *gen.Queries
	repoBaseDir string // host-side base dir; paths under it are rewritten to /repos inside the container
}

func NewReposHandler(q *gen.Queries, repoBaseDir string) *ReposHandler {
	return &ReposHandler{q: q, repoBaseDir: repoBaseDir}
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

func (h *ReposHandler) Create(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name       string  `json:"name"`
		Path       string  `json:"path"`
		RemoteURL  *string `json:"remote_url"`
		WorkflowID *string `json:"workflow_id"`
	}
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

	// Auto-clone when no local path is provided.
	if body.Path == "" {
		if remoteURL == "" {
			Err(w, http.StatusBadRequest, "path or remote_url is required")
			return
		}
		if h.repoBaseDir == "" {
			Err(w, http.StatusBadRequest, "repo_base_dir must be configured on the server to enable auto-cloning")
			return
		}

		// Derive the clone destination from the parsed name (org/repo) or fall back
		// to the last path segment of the remote URL.
		cloneSubdir := body.Name
		if cloneSubdir == "" {
			// Name not yet known; use last segment of URL as subdir.
			seg := remoteURL[strings.LastIndex(remoteURL, "/")+1:]
			cloneSubdir = strings.TrimSuffix(seg, ".git")
		}
		destPath := filepath.Join(h.repoBaseDir, cloneSubdir)

		// Validate destPath is within repoBaseDir BEFORE any filesystem operations
		// to prevent path traversal via a crafted name or URL segment (e.g. "../../etc").
		{
			cleanDest := filepath.Clean(destPath)
			cleanBase := filepath.Clean(h.repoBaseDir)
			sep := string(os.PathSeparator)
			if cleanDest != cleanBase && !strings.HasPrefix(cleanDest+sep, cleanBase+sep) {
				Err(w, http.StatusBadRequest, "derived clone path is outside the allowed base directory")
				return
			}
		}

		// Only allow https:// and git@ schemes to avoid unexpected behaviour.
		if !strings.HasPrefix(remoteURL, "https://") && !strings.HasPrefix(remoteURL, "git@") {
			Err(w, http.StatusBadRequest, "remote_url must use https:// or git@ scheme")
			return
		}

		// Create parent directory structure (e.g. repoBaseDir/org/).
		if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
			Err(w, http.StatusInternalServerError, fmt.Sprintf("failed to create parent directory: %v", err))
			return
		}

		cloneCmd := exec.CommandContext(r.Context(), "git", "clone", remoteURL, destPath)
		if out, err := cloneCmd.CombinedOutput(); err != nil {
			Err(w, http.StatusBadRequest, fmt.Sprintf("git clone failed: %s", strings.TrimSpace(string(out))))
			return
		}

		body.Path = destPath
	}

	// name must be known by now.
	if body.Name == "" {
		Err(w, http.StatusBadRequest, "name is required (or provide a GitHub remote_url for auto-detection)")
		return
	}

	// If a host-to-container path mapping is configured, transparently rewrite host paths.
	// Enforce base-dir restriction when configured.
	if h.repoBaseDir != "" {
		sep := string(os.PathSeparator)
		base := filepath.Clean(h.repoBaseDir)
		clean := filepath.Clean(body.Path)
		if clean != base && !strings.HasPrefix(clean+sep, base+sep) {
			Err(w, http.StatusBadRequest, "repo path is outside the allowed base directory")
			return
		}
	}

	// Verify the path exists and is a git repository before persisting.
	if err := exec.CommandContext(r.Context(), "git", "-C", body.Path, "rev-parse", "--git-dir").Run(); err != nil {
		Err(w, http.StatusBadRequest, "path is not a git repository")
		return
	}

	repo, err := h.q.CreateRepo(r.Context(), gen.CreateRepoParams{
		ID:         uuid.NewString(),
		Name:       body.Name,
		Path:       body.Path,
		RemoteUrl:  body.RemoteURL,
		WorkflowID: body.WorkflowID,
	})
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	setClaudeTrust(body.Path)
	JSON(w, http.StatusCreated, repo)
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
		Name       *string `json:"name"`
		Path       *string `json:"path"`
		RemoteURL  *string `json:"remote_url"`
		WorkflowID *string `json:"workflow_id"`
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
	if h.repoBaseDir != "" {
		realPath, err := filepath.EvalSymlinks(path)
		if err != nil {
			realPath = filepath.Clean(path)
		}
		realBase, err := filepath.EvalSymlinks(h.repoBaseDir)
		if err != nil {
			realBase = filepath.Clean(h.repoBaseDir)
		}
		sep := string(os.PathSeparator)
		if realPath != realBase && !strings.HasPrefix(realPath+sep, realBase+sep) {
			Err(w, http.StatusBadRequest, "repo path is outside the allowed base directory")
			return
		}
	}

	// Verify the path is still a valid git repository.
	if err := exec.CommandContext(r.Context(), "git", "-C", path, "rev-parse", "--git-dir").Run(); err != nil {
		Err(w, http.StatusBadRequest, "path is not a git repository")
		return
	}

	repo, err := h.q.UpdateRepo(r.Context(), gen.UpdateRepoParams{
		ID:         id,
		Name:       name,
		Path:       path,
		RemoteUrl:  remoteURL,
		WorkflowID: workflowID,
	})
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	setClaudeTrust(path)
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
func setClaudeTrust(repoPath string) {
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
	if err := os.WriteFile(claudeJSON, out, 0o600); err != nil {
		slog.Warn("failed to update claude trust dialog", "path", repoPath, "err", err)
	}
}
