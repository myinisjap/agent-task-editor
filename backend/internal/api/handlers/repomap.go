package handlers

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/myinisjap/agent-task-editor/backend/internal/api/middleware"
	"github.com/myinisjap/agent-task-editor/backend/internal/repomap"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

// RepoMapHandler serves the per-repo map (docs/repo-map.md): read the pages
// and staleness, configure the opt-in, trigger generation, list runs, and edit
// the global injection settings.
type RepoMapHandler struct {
	q   *gen.Queries
	svc *repomap.Service
}

func NewRepoMapHandler(q *gen.Queries, svc *repomap.Service) *RepoMapHandler {
	return &RepoMapHandler{q: q, svc: svc}
}

type repoMapPageResponse struct {
	Slug      string `json:"slug"`
	Title     string `json:"title"`
	Body      string `json:"body"`
	CommitSha string `json:"commit_sha"`
}

type repoMapConfigResponse struct {
	Enabled              bool    `json:"enabled"`
	ProviderConfigID     *string `json:"provider_config_id"`
	RefreshIntervalHours int64   `json:"refresh_interval_hours"`
	MinCommitsBehind     int64   `json:"min_commits_behind"`
}

type repoMapRunResponse struct {
	ID               string     `json:"id"`
	RepoID           string     `json:"repo_id"`
	Trigger          string     `json:"trigger"`
	Actor            string     `json:"actor"`
	Status           string     `json:"status"`
	CommitSha        string     `json:"commit_sha"`
	ProviderConfigID *string    `json:"provider_config_id"`
	PageCount        int64      `json:"page_count"`
	CharCount        int64      `json:"char_count"`
	Error            string     `json:"error"`
	CostUsd          float64    `json:"cost_usd"`
	InputTokens      int64      `json:"input_tokens"`
	OutputTokens     int64      `json:"output_tokens"`
	StartedAt        time.Time  `json:"started_at"`
	FinishedAt       *time.Time `json:"finished_at"`
}

type repoMapResponse struct {
	Config repoMapConfigResponse `json:"config"`
	Pages  []repoMapPageResponse `json:"pages"`
	// CommitSha/GeneratedAt describe the successful run the pages came from.
	CommitSha   string     `json:"commit_sha"`
	GeneratedAt *time.Time `json:"generated_at"`
	HeadSha     string     `json:"head_sha"`
	// CommitsBehind is null when unknown or when there is no map.
	CommitsBehind *int `json:"commits_behind"`
	LayoutChanged bool `json:"layout_changed"`
	// Stale is the scheduled-refresh rule applied right now.
	Stale   bool                `json:"stale"`
	Running bool                `json:"running"`
	LastRun *repoMapRunResponse `json:"last_run"`
}

func toRepoMapRunResponse(r gen.RepoMapRun) repoMapRunResponse {
	return repoMapRunResponse{
		ID: r.ID, RepoID: r.RepoID, Trigger: r.Trigger, Actor: r.Actor, Status: r.Status, CommitSha: r.CommitSha,
		ProviderConfigID: r.ProviderConfigID, PageCount: r.PageCount, CharCount: r.CharCount, Error: r.Error,
		CostUsd: r.CostUsd, InputTokens: r.InputTokens, OutputTokens: r.OutputTokens,
		StartedAt: r.StartedAt, FinishedAt: r.FinishedAt,
	}
}

func repoMapConfigOrDefault(cfg gen.RepoMapConfig, err error) (gen.RepoMapConfig, error) {
	if errors.Is(err, sql.ErrNoRows) {
		return gen.RepoMapConfig{RefreshIntervalHours: repomap.DefaultIntervalHours, MinCommitsBehind: repomap.DefaultMinCommits}, nil
	}
	return cfg, err
}

func (h *RepoMapHandler) repoExists(w http.ResponseWriter, r *http.Request) (string, bool) {
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

// Get returns the repo's current map, its config and staleness.
//
// GET /api/v1/repos/{id}/map
func (h *RepoMapHandler) Get(w http.ResponseWriter, r *http.Request) {
	id, ok := h.repoExists(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	cfg, err := repoMapConfigOrDefault(h.q.GetRepoMapConfig(ctx, id))
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	m, err := h.svc.Current(ctx, id)
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	resp := repoMapResponse{
		Config: repoMapConfigResponse{
			Enabled: cfg.Enabled != 0, ProviderConfigID: cfg.ProviderConfigID,
			RefreshIntervalHours: cfg.RefreshIntervalHours, MinCommitsBehind: cfg.MinCommitsBehind,
		},
		Pages: []repoMapPageResponse{},
	}
	if m.Run != nil {
		resp.CommitSha, resp.GeneratedAt, resp.HeadSha = m.Run.CommitSha, m.Run.FinishedAt, m.HeadSHA
		resp.LayoutChanged = m.LayoutChanged
		if m.CommitsBehind >= 0 {
			n := m.CommitsBehind
			resp.CommitsBehind = &n
		}
		resp.Stale = repomap.NeedsRefresh(true, m.CommitsBehind, int(cfg.MinCommitsBehind), m.LayoutChanged)
		for _, p := range m.Pages {
			resp.Pages = append(resp.Pages, repoMapPageResponse{Slug: p.Slug, Title: p.Title, Body: p.Body, CommitSha: p.CommitSha})
		}
	}
	if last, err := h.q.GetLatestRepoMapRun(ctx, id); err == nil {
		lr := toRepoMapRunResponse(last)
		resp.LastRun = &lr
		resp.Running = last.Status == repomap.RunRunning
	}
	JSON(w, http.StatusOK, resp)
}

type repoMapConfigBody struct {
	Enabled              *bool   `json:"enabled"`
	ProviderConfigID     *string `json:"provider_config_id"`
	RefreshIntervalHours *int64  `json:"refresh_interval_hours"`
	MinCommitsBehind     *int64  `json:"min_commits_behind"`
}

// UpdateConfig sets the per-repo opt-in and refresh policy (partial update).
//
// PUT /api/v1/repos/{id}/map/config
func (h *RepoMapHandler) UpdateConfig(w http.ResponseWriter, r *http.Request) {
	id, ok := h.repoExists(w, r)
	if !ok {
		return
	}
	var body repoMapConfigBody
	if err := decode(r, &body); err != nil {
		Err(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	ctx := r.Context()
	cfg, err := repoMapConfigOrDefault(h.q.GetRepoMapConfig(ctx, id))
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	if body.Enabled != nil {
		cfg.Enabled = 0
		if *body.Enabled {
			cfg.Enabled = 1
		}
	}
	if body.ProviderConfigID != nil {
		pid := strings.TrimSpace(*body.ProviderConfigID)
		if pid == "" {
			cfg.ProviderConfigID = nil
		} else {
			if _, err := h.q.GetProviderConfig(ctx, pid); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					Err(w, http.StatusBadRequest, "provider config not found")
				} else {
					Err(w, http.StatusInternalServerError, err.Error())
				}
				return
			}
			cfg.ProviderConfigID = &pid
		}
	}
	if body.RefreshIntervalHours != nil {
		if *body.RefreshIntervalHours < 0 || *body.RefreshIntervalHours > 24*365 {
			Err(w, http.StatusBadRequest, "refresh_interval_hours must be between 0 (manual only) and 8760")
			return
		}
		cfg.RefreshIntervalHours = *body.RefreshIntervalHours
	}
	if body.MinCommitsBehind != nil {
		if *body.MinCommitsBehind < 0 || *body.MinCommitsBehind > 100000 {
			Err(w, http.StatusBadRequest, "min_commits_behind must be between 0 and 100000")
			return
		}
		cfg.MinCommitsBehind = *body.MinCommitsBehind
	}
	if cfg.Enabled != 0 && cfg.ProviderConfigID == nil {
		Err(w, http.StatusBadRequest, "choose a provider config before enabling the repo map")
		return
	}
	saved, err := h.q.UpsertRepoMapConfig(ctx, gen.UpsertRepoMapConfigParams{
		RepoID: id, Enabled: cfg.Enabled, ProviderConfigID: cfg.ProviderConfigID,
		RefreshIntervalHours: cfg.RefreshIntervalHours, MinCommitsBehind: cfg.MinCommitsBehind,
	})
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	JSON(w, http.StatusOK, repoMapConfigResponse{
		Enabled: saved.Enabled != 0, ProviderConfigID: saved.ProviderConfigID,
		RefreshIntervalHours: saved.RefreshIntervalHours, MinCommitsBehind: saved.MinCommitsBehind,
	})
}

// Generate starts a manual generation run in the background.
//
// POST /api/v1/repos/{id}/map/generate
func (h *RepoMapHandler) Generate(w http.ResponseWriter, r *http.Request) {
	run, err := h.svc.Start(r.Context(), chi.URLParam(r, "id"), middleware.ActorFromContext(r.Context()))
	switch {
	case err == nil:
		JSON(w, http.StatusAccepted, toRepoMapRunResponse(run))
	case errors.Is(err, repomap.ErrNotFound):
		Err(w, http.StatusNotFound, "repo not found")
	case errors.Is(err, repomap.ErrDisabled), errors.Is(err, repomap.ErrNoProvider), errors.Is(err, repomap.ErrNoModel):
		Err(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, repomap.ErrRunning), errors.Is(err, repomap.ErrCostCeiling):
		Err(w, http.StatusConflict, err.Error())
	default:
		Err(w, http.StatusInternalServerError, err.Error())
	}
}

// ListRuns returns the repo's generation runs, newest first.
//
// GET /api/v1/repos/{id}/map/runs?limit=
func (h *RepoMapHandler) ListRuns(w http.ResponseWriter, r *http.Request) {
	id, ok := h.repoExists(w, r)
	if !ok {
		return
	}
	limit := int64(10)
	if s := r.URL.Query().Get("limit"); s != "" {
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil || n < 1 || n > 100 {
			Err(w, http.StatusBadRequest, "limit must be between 1 and 100")
			return
		}
		limit = n
	}
	rows, err := h.q.ListRepoMapRuns(r.Context(), gen.ListRepoMapRunsParams{RepoID: id, Limit: limit})
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]repoMapRunResponse, 0, len(rows))
	for _, row := range rows {
		out = append(out, toRepoMapRunResponse(row))
	}
	JSON(w, http.StatusOK, out)
}

type repoMapSettingsResponse struct {
	InjectLabels     []string `json:"inject_labels"`
	ExcludedAgentIDs []string `json:"excluded_agent_ids"`
}

func toRepoMapSettings(g gen.RepoMapGlobal) repoMapSettingsResponse {
	ids := []string{}
	_ = json.Unmarshal([]byte(g.ExcludedAgentIds), &ids)
	if ids == nil {
		ids = []string{}
	}
	return repoMapSettingsResponse{InjectLabels: repomap.SplitLabels(g.InjectLabels), ExcludedAgentIDs: ids}
}

// GetSettings returns the global injection settings.
//
// GET /api/v1/settings/repo-map
func (h *RepoMapHandler) GetSettings(w http.ResponseWriter, r *http.Request) {
	g, err := h.q.GetRepoMapGlobal(r.Context())
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	JSON(w, http.StatusOK, toRepoMapSettings(g))
}

// UpdateSettings replaces the labels flagged for injection and the agent
// configs that opted out.
//
// PUT /api/v1/settings/repo-map
func (h *RepoMapHandler) UpdateSettings(w http.ResponseWriter, r *http.Request) {
	var body struct {
		InjectLabels     *[]string `json:"inject_labels"`
		ExcludedAgentIDs *[]string `json:"excluded_agent_ids"`
	}
	if err := decode(r, &body); err != nil {
		Err(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	ctx := r.Context()
	cur, err := h.q.GetRepoMapGlobal(ctx)
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	labels, excluded := cur.InjectLabels, cur.ExcludedAgentIds
	if body.InjectLabels != nil {
		clean := make([]string, 0, len(*body.InjectLabels))
		for _, l := range *body.InjectLabels {
			l = strings.TrimSpace(l)
			if l == "" {
				continue
			}
			if strings.ContainsAny(l, ",\n\r") {
				Err(w, http.StatusBadRequest, "label names may not contain commas or newlines")
				return
			}
			clean = append(clean, l)
		}
		labels = strings.Join(clean, ",")
	}
	if body.ExcludedAgentIDs != nil {
		ids := make([]string, 0, len(*body.ExcludedAgentIDs))
		for _, id := range *body.ExcludedAgentIDs {
			if id = strings.TrimSpace(id); id != "" {
				ids = append(ids, id)
			}
		}
		b, _ := json.Marshal(ids)
		excluded = string(b)
	}
	saved, err := h.q.UpdateRepoMapGlobal(ctx, gen.UpdateRepoMapGlobalParams{InjectLabels: labels, ExcludedAgentIds: excluded})
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	JSON(w, http.StatusOK, toRepoMapSettings(saved))
}
