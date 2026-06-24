package handlers

import (
	"net/http"
	"os/exec"
	"regexp"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

// validGitRef matches HEAD, HEAD~N, HEAD^N, 40/64-char hex SHAs, and safe branch/tag names.
var validGitRef = regexp.MustCompile(`^(HEAD([~^][0-9]+)?|[0-9a-f]{40,64}|[a-zA-Z0-9._/-]+)$`)

func isValidGitRef(ref string) bool {
	return validGitRef.MatchString(ref) && !strings.Contains(ref, "..")
}

type ReposHandler struct {
	q *gen.Queries
}

func NewReposHandler(q *gen.Queries) *ReposHandler {
	return &ReposHandler{q: q}
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
	if body.Name == "" || body.Path == "" {
		Err(w, http.StatusBadRequest, "name and path are required")
		return
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
	JSON(w, http.StatusCreated, repo)
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

	out, err := exec.CommandContext(r.Context(), "git", "-C", repo.Path, "ls-tree", "-r", "--name-only", ref).Output()
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

func (h *ReposHandler) Diff(w http.ResponseWriter, r *http.Request) {
	repo, err := h.q.GetRepo(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		Err(w, http.StatusNotFound, "repo not found")
		return
	}

	base := r.URL.Query().Get("base")
	head := r.URL.Query().Get("head")
	if base == "" {
		base = "HEAD~1"
	}
	if head == "" {
		head = "HEAD"
	}
	if !isValidGitRef(base) || !isValidGitRef(head) {
		Err(w, http.StatusBadRequest, "invalid git ref")
		return
	}

	out, err := exec.CommandContext(r.Context(), "git", "-C", repo.Path, "diff", base, head).Output()
	if err != nil {
		Err(w, http.StatusInternalServerError, "failed to compute diff")
		return
	}

	JSON(w, http.StatusOK, map[string]any{
		"base": base,
		"head": head,
		"diff": string(out),
	})
}
