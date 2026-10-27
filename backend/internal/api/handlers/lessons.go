package handlers

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/myinisjap/agent-task-editor/backend/internal/agent"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

// MaxLessonBodyLen is the maximum lesson body length in characters.
const MaxLessonBodyLen = 300

var (
	lessonKinds    = map[string]bool{"convention": true, "trap": true, "command": true, "decision": true, "preference": true}
	lessonStatuses = map[string]bool{"proposed": true, "active": true, "stale": true, "retired": true}
	lessonSources  = map[string]bool{"human": true, "agent": true, "distiller": true}
)

// LessonsHandler manages repo lessons (per-repo or global when repo_id is NULL).
// Lists are small and curated, so they are not paginated.
type LessonsHandler struct {
	q *gen.Queries
}

func NewLessonsHandler(q *gen.Queries) *LessonsHandler { return &LessonsHandler{q: q} }

type lessonResponse struct {
	ID            string          `json:"id"`
	RepoID        *string         `json:"repo_id"`
	Kind          string          `json:"kind"`
	Body          string          `json:"body"`
	PathGlobs     []string        `json:"path_globs"`
	Stages        []string        `json:"stages"`
	Citations     json.RawMessage `json:"citations"`
	Source        string          `json:"source"`
	SourceTaskID  *string         `json:"source_task_id"`
	Status        string          `json:"status"`
	InjectedCount int64           `json:"injected_count"`
	CreatedAt     time.Time       `json:"created_at"`
	UpdatedAt     time.Time       `json:"updated_at"`
	LastCheckedAt *time.Time      `json:"last_checked_at"`
}

func toLessonResponse(l gen.RepoLesson) lessonResponse {
	cit := json.RawMessage(l.Citations)
	if !json.Valid(cit) {
		cit = json.RawMessage("[]")
	}
	return lessonResponse{
		ID: l.ID, RepoID: l.RepoID, Kind: l.Kind, Body: l.Body,
		PathGlobs: agent.SplitLessonList(l.PathGlobs), Stages: agent.SplitLessonList(l.Stages),
		Citations: cit, Source: l.Source, SourceTaskID: l.SourceTaskID, Status: l.Status,
		InjectedCount: l.InjectedCount, CreatedAt: l.CreatedAt, UpdatedAt: l.UpdatedAt,
		LastCheckedAt: l.LastCheckedAt,
	}
}

func toLessonResponses(ls []gen.RepoLesson) []lessonResponse {
	out := make([]lessonResponse, 0, len(ls))
	for _, l := range ls {
		out = append(out, toLessonResponse(l))
	}
	return out
}

type lessonBody struct {
	Kind         *string          `json:"kind"`
	Body         *string          `json:"body"`
	PathGlobs    *[]string        `json:"path_globs"`
	Stages       *[]string        `json:"stages"`
	Citations    *json.RawMessage `json:"citations"`
	Status       *string          `json:"status"`
	Source       *string          `json:"source"`
	SourceTaskID *string          `json:"source_task_id"`
}

// normList trims, dedupes and rejects entries containing separators.
func normList(in []string) ([]string, bool) {
	seen := map[string]bool{}
	out := []string{}
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if strings.ContainsAny(s, "\n\r,") {
			return nil, false
		}
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out, true
}

// apply validates b and merges it onto p. Returns an error message or "".
func (b lessonBody) apply(p *gen.UpdateRepoLessonParams) string {
	if b.Kind != nil {
		if !lessonKinds[*b.Kind] {
			return "invalid kind"
		}
		p.Kind = *b.Kind
	}
	if b.Status != nil {
		if !lessonStatuses[*b.Status] {
			return "invalid status"
		}
		p.Status = *b.Status
	}
	if b.Source != nil {
		if !lessonSources[*b.Source] {
			return "invalid source"
		}
		p.Source = *b.Source
	}
	if b.Body != nil {
		body := strings.TrimSpace(*b.Body)
		if body == "" {
			return "body is required"
		}
		if utf8.RuneCountInString(body) > MaxLessonBodyLen {
			return "body must be at most 300 characters"
		}
		p.Body = body
	}
	if b.PathGlobs != nil {
		l, ok := normList(*b.PathGlobs)
		if !ok {
			return "path_globs entries must not contain commas or newlines"
		}
		p.PathGlobs = strings.Join(l, "\n")
	}
	if b.Stages != nil {
		l, ok := normList(*b.Stages)
		if !ok {
			return "stages entries must not contain commas or newlines"
		}
		p.Stages = strings.Join(l, ",")
	}
	if b.Citations != nil {
		var arr []json.RawMessage
		if err := json.Unmarshal(*b.Citations, &arr); err != nil || arr == nil {
			return "citations must be a JSON array"
		}
		p.Citations = string(*b.Citations)
	}
	if b.SourceTaskID != nil {
		v := *b.SourceTaskID
		p.SourceTaskID = &v
	}
	return ""
}

func validStatusFilter(w http.ResponseWriter, r *http.Request) (string, bool) {
	s := r.URL.Query().Get("status")
	if s != "" && !lessonStatuses[s] {
		Err(w, http.StatusBadRequest, "invalid status")
		return "", false
	}
	return s, true
}

func (h *LessonsHandler) repoExists(w http.ResponseWriter, r *http.Request) (string, bool) {
	id := chi.URLParam(r, "id")
	if _, err := h.q.GetRepo(r.Context(), id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			Err(w, http.StatusNotFound, "repo not found")
		} else {
			Err(w, http.StatusInternalServerError, err.Error())
		}
		return "", false
	}
	return id, true
}

func (h *LessonsHandler) ListForRepo(w http.ResponseWriter, r *http.Request) {
	id, ok := h.repoExists(w, r)
	if !ok {
		return
	}
	status, ok := validStatusFilter(w, r)
	if !ok {
		return
	}
	ls, err := h.q.ListRepoLessons(r.Context(), gen.ListRepoLessonsParams{RepoID: &id, Column2: status})
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	JSON(w, http.StatusOK, toLessonResponses(ls))
}

func (h *LessonsHandler) ListGlobal(w http.ResponseWriter, r *http.Request) {
	status, ok := validStatusFilter(w, r)
	if !ok {
		return
	}
	ls, err := h.q.ListGlobalLessons(r.Context(), status)
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	JSON(w, http.StatusOK, toLessonResponses(ls))
}

func (h *LessonsHandler) create(w http.ResponseWriter, r *http.Request, repoID *string) {
	var b lessonBody
	if err := decode(r, &b); err != nil {
		Err(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if b.Kind == nil || *b.Kind == "" {
		Err(w, http.StatusBadRequest, "kind is required")
		return
	}
	if b.Body == nil {
		Err(w, http.StatusBadRequest, "body is required")
		return
	}
	p := gen.UpdateRepoLessonParams{Citations: "[]", Status: "active", Source: "human"}
	if msg := b.apply(&p); msg != "" {
		Err(w, http.StatusBadRequest, msg)
		return
	}
	l, err := h.q.CreateRepoLesson(r.Context(), gen.CreateRepoLessonParams{
		ID: uuid.NewString(), RepoID: repoID, Kind: p.Kind, Body: p.Body,
		PathGlobs: p.PathGlobs, Stages: p.Stages, Citations: p.Citations,
		Source: p.Source, SourceTaskID: p.SourceTaskID, Status: p.Status,
	})
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	JSON(w, http.StatusCreated, toLessonResponse(l))
}

func (h *LessonsHandler) CreateForRepo(w http.ResponseWriter, r *http.Request) {
	id, ok := h.repoExists(w, r)
	if !ok {
		return
	}
	h.create(w, r, &id)
}

func (h *LessonsHandler) CreateGlobal(w http.ResponseWriter, r *http.Request) {
	h.create(w, r, nil)
}

func (h *LessonsHandler) Update(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	cur, err := h.q.GetRepoLesson(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			Err(w, http.StatusNotFound, "lesson not found")
		} else {
			Err(w, http.StatusInternalServerError, err.Error())
		}
		return
	}
	var b lessonBody
	if err := decode(r, &b); err != nil {
		Err(w, http.StatusBadRequest, "invalid request body")
		return
	}
	p := gen.UpdateRepoLessonParams{
		ID: id, Kind: cur.Kind, Body: cur.Body, PathGlobs: cur.PathGlobs, Stages: cur.Stages,
		Citations: cur.Citations, Status: cur.Status, Source: cur.Source, SourceTaskID: cur.SourceTaskID,
	}
	if msg := b.apply(&p); msg != "" {
		Err(w, http.StatusBadRequest, msg)
		return
	}
	l, err := h.q.UpdateRepoLesson(r.Context(), p)
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	JSON(w, http.StatusOK, toLessonResponse(l))
}

func (h *LessonsHandler) Delete(w http.ResponseWriter, r *http.Request) {
	n, err := h.q.DeleteRepoLesson(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	if n == 0 {
		Err(w, http.StatusNotFound, "lesson not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
