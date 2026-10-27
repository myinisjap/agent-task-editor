package handlers

import (
	"database/sql"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/myinisjap/agent-task-editor/backend/internal/skills"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

// SkillsHandler manages the skills library and its attachment to agent
// configs and repos. Skills are trusted admin config: these routes are only
// reachable with the normal API token (never the MCP sidecar's scoped tokens).
type SkillsHandler struct {
	q     *gen.Queries
	db    *sql.DB
	store skills.Store
}

func NewSkillsHandler(q *gen.Queries, db *sql.DB, store skills.Store) *SkillsHandler {
	return &SkillsHandler{q: q, db: db, store: store}
}

type skillResponse struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Enabled     bool      `json:"enabled"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	// SkillMD and Files are only populated by GET /skills/{id}.
	SkillMD string   `json:"skill_md,omitempty"`
	Files   []string `json:"files,omitempty"`
}

func toSkillResponse(s gen.Skill) skillResponse {
	return skillResponse{ID: s.ID, Name: s.Name, Description: s.Description, Enabled: s.Enabled != 0, CreatedAt: s.CreatedAt, UpdatedAt: s.UpdatedAt}
}

func toSkillResponses(in []gen.Skill) []skillResponse {
	out := make([]skillResponse, 0, len(in))
	for _, s := range in {
		out = append(out, toSkillResponse(s))
	}
	return out
}

func (h *SkillsHandler) List(w http.ResponseWriter, r *http.Request) {
	list, err := h.q.ListSkills(r.Context())
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	JSON(w, http.StatusOK, toSkillResponses(list))
}

func (h *SkillsHandler) Get(w http.ResponseWriter, r *http.Request) {
	s, err := h.q.GetSkill(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		Err(w, http.StatusNotFound, "skill not found")
		return
	}
	resp := toSkillResponse(s)
	if md, err := readSkillMD(h.store, s.Name); err == nil {
		resp.SkillMD = md
	}
	resp.Files, _ = h.store.ReadFiles(s.Name)
	JSON(w, http.StatusOK, resp)
}

// readBundle parses the request body: application/zip is a zipped skill
// folder, anything else is JSON {"skill_md": "..."}.
func readBundle(r *http.Request) (*skills.Bundle, error) {
	mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if strings.ToLower(mt) == "application/zip" {
		data, err := io.ReadAll(r.Body)
		if err != nil {
			return nil, errors.New("request body too large or unreadable")
		}
		return skills.BundleFromZip(data)
	}
	var body struct {
		SkillMD string `json:"skill_md"`
	}
	if err := decode(r, &body); err != nil {
		return nil, errors.New("invalid request body")
	}
	if body.SkillMD == "" {
		return nil, errors.New("skill_md is required")
	}
	return skills.BundleFromSkillMD([]byte(body.SkillMD))
}

func (h *SkillsHandler) Create(w http.ResponseWriter, r *http.Request) {
	b, err := readBundle(r)
	if err != nil {
		Err(w, http.StatusBadRequest, err.Error())
		return
	}
	s, err := h.q.CreateSkill(r.Context(), gen.CreateSkillParams{ID: uuid.NewString(), Name: b.Name, Description: b.Description, Enabled: 1})
	if err != nil {
		Err(w, http.StatusConflict, "a skill with that name already exists")
		return
	}
	if err := h.store.Write(b); err != nil {
		_ = h.q.DeleteSkill(r.Context(), s.ID)
		Err(w, http.StatusInternalServerError, "store skill: "+err.Error())
		return
	}
	JSON(w, http.StatusCreated, toSkillResponse(s))
}

// Update toggles enabled and/or replaces the skill's content. Replacement
// content must keep the skill's name. The JSON body carries {"enabled": bool, "skill_md": "..."}, or
// the body is a zip (content replacement only).
func (h *SkillsHandler) Update(w http.ResponseWriter, r *http.Request) {
	cur, err := h.q.GetSkill(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		Err(w, http.StatusNotFound, "skill not found")
		return
	}
	var bundle *skills.Bundle
	enabled := cur.Enabled
	mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if strings.ToLower(mt) == "application/zip" {
		bundle, err = readBundle(r)
		if err != nil {
			Err(w, http.StatusBadRequest, err.Error())
			return
		}
	} else {
		var body struct {
			Enabled *bool   `json:"enabled"`
			SkillMD *string `json:"skill_md"`
		}
		if err := decode(r, &body); err != nil {
			Err(w, http.StatusBadRequest, "invalid request body")
			return
		}
		if body.Enabled != nil {
			enabled = 0
			if *body.Enabled {
				enabled = 1
			}
		}
		if body.SkillMD != nil {
			bundle, err = skills.BundleFromSkillMD([]byte(*body.SkillMD))
			if err != nil {
				Err(w, http.StatusBadRequest, err.Error())
				return
			}
		}
	}
	desc := cur.Description
	if bundle != nil {
		if bundle.Name != cur.Name {
			Err(w, http.StatusBadRequest, "frontmatter name must match the existing skill name ("+cur.Name+")")
			return
		}
		if err := h.store.Write(bundle); err != nil {
			Err(w, http.StatusInternalServerError, "store skill: "+err.Error())
			return
		}
		desc = bundle.Description
	}
	s, err := h.q.UpdateSkill(r.Context(), gen.UpdateSkillParams{Description: desc, Enabled: enabled, ID: cur.ID})
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	JSON(w, http.StatusOK, toSkillResponse(s))
}

func (h *SkillsHandler) Delete(w http.ResponseWriter, r *http.Request) {
	s, err := h.q.GetSkill(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		Err(w, http.StatusNotFound, "skill not found")
		return
	}
	if err := h.q.DeleteSkill(r.Context(), s.ID); err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := h.store.Remove(s.Name); err != nil {
		slog.Warn("skills: remove folder", "skill", s.Name, "err", err)
	}
	w.WriteHeader(http.StatusNoContent)
}

type skillIDsBody struct {
	SkillIDs []string `json:"skill_ids"`
}

func (h *SkillsHandler) ListForAgent(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, err := h.q.GetAgentConfig(r.Context(), id); err != nil {
		Err(w, http.StatusNotFound, "agent config not found")
		return
	}
	list, err := h.q.ListAgentConfigSkills(r.Context(), id)
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	JSON(w, http.StatusOK, toSkillResponses(list))
}

func (h *SkillsHandler) SetForAgent(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, err := h.q.GetAgentConfig(r.Context(), id); err != nil {
		Err(w, http.StatusNotFound, "agent config not found")
		return
	}
	h.replace(w, r, func(q *gen.Queries, sid string) error {
		return q.AddAgentConfigSkill(r.Context(), gen.AddAgentConfigSkillParams{AgentConfigID: id, SkillID: sid})
	}, func(q *gen.Queries) error { return q.ClearAgentConfigSkills(r.Context(), id) },
		func() ([]gen.Skill, error) { return h.q.ListAgentConfigSkills(r.Context(), id) })
}

func (h *SkillsHandler) ListForRepo(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, err := h.q.GetRepo(r.Context(), id); err != nil {
		Err(w, http.StatusNotFound, "repo not found")
		return
	}
	list, err := h.q.ListRepoSkills(r.Context(), id)
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	JSON(w, http.StatusOK, toSkillResponses(list))
}

func (h *SkillsHandler) SetForRepo(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, err := h.q.GetRepo(r.Context(), id); err != nil {
		Err(w, http.StatusNotFound, "repo not found")
		return
	}
	h.replace(w, r, func(q *gen.Queries, sid string) error {
		return q.AddRepoSkill(r.Context(), gen.AddRepoSkillParams{RepoID: id, SkillID: sid})
	}, func(q *gen.Queries) error { return q.ClearRepoSkills(r.Context(), id) },
		func() ([]gen.Skill, error) { return h.q.ListRepoSkills(r.Context(), id) })
}

// replace atomically swaps the attached skill set with the request's skill_ids.
func (h *SkillsHandler) replace(w http.ResponseWriter, r *http.Request, add func(*gen.Queries, string) error, clear func(*gen.Queries) error, list func() ([]gen.Skill, error)) {
	var body skillIDsBody
	if err := decode(r, &body); err != nil {
		Err(w, http.StatusBadRequest, "invalid request body")
		return
	}
	for _, sid := range body.SkillIDs {
		if _, err := h.q.GetSkill(r.Context(), sid); err != nil {
			Err(w, http.StatusBadRequest, "unknown skill id: "+sid)
			return
		}
	}
	tx, err := h.db.BeginTx(r.Context(), nil)
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer func() { _ = tx.Rollback() }()
	tq := h.q.WithTx(tx)
	if err := clear(tq); err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	for _, sid := range body.SkillIDs {
		if err := add(tq, sid); err != nil {
			Err(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	if err := tx.Commit(); err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	out, err := list()
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	JSON(w, http.StatusOK, toSkillResponses(out))
}

func readSkillMD(store skills.Store, name string) (string, error) {
	b, err := os.ReadFile(filepath.Join(store.Path(name), "SKILL.md"))
	return string(b), err
}
