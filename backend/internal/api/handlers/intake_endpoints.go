package handlers

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/myinisjap/agent-task-editor/backend/internal/api/middleware"
	"github.com/myinisjap/agent-task-editor/backend/internal/intake"
	"github.com/myinisjap/agent-task-editor/backend/internal/intakehook"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

const (
	intakeMaxBody       = 256 << 10
	intakeRatePerMinute = 30
	intakeSigHeader     = "X-ATE-Signature-256"
)

// endpointLimiter is a fixed-window per-endpoint rate limiter.
type endpointLimiter struct {
	mu      sync.Mutex
	now     func() time.Time
	limit   int
	window  time.Duration
	buckets map[string]*limiterBucket
}

type limiterBucket struct {
	start time.Time
	count int
}

func newEndpointLimiter(limit int, window time.Duration) *endpointLimiter {
	return &endpointLimiter{now: time.Now, limit: limit, window: window, buckets: map[string]*limiterBucket{}}
}

// allow reports whether another request is allowed; when not, retryAfter is
// the time until the window resets.
func (l *endpointLimiter) allow(id string) (ok bool, retryAfter time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	b := l.buckets[id]
	if b == nil || now.Sub(b.start) >= l.window {
		l.buckets[id] = &limiterBucket{start: now, count: 1}
		return true, 0
	}
	if b.count >= l.limit {
		return false, b.start.Add(l.window).Sub(now)
	}
	b.count++
	return true, 0
}

// IntakeEndpointsHandler manages intake endpoints and receives their signed
// webhook deliveries.
type IntakeEndpointsHandler struct {
	q       *gen.Queries
	svc     *intakehook.Service
	limiter *endpointLimiter
}

func NewIntakeEndpointsHandler(q *gen.Queries, svc *intakehook.Service) *IntakeEndpointsHandler {
	return &IntakeEndpointsHandler{q: q, svc: svc, limiter: newEndpointLimiter(intakeRatePerMinute, time.Minute)}
}

// intakeEndpointDTO is the API view of an endpoint; it never carries the secret.
type intakeEndpointDTO struct {
	ID                  string     `json:"id"`
	Name                string     `json:"name"`
	RepoID              string     `json:"repo_id"`
	WorkflowID          *string    `json:"workflow_id"`
	TargetLabel         string     `json:"target_label"`
	Enabled             bool       `json:"enabled"`
	TitleTemplate       string     `json:"title_template"`
	DescriptionTemplate string     `json:"description_template"`
	DedupeKeyTemplate   string     `json:"dedupe_key_template"`
	LastReceivedAt      *time.Time `json:"last_received_at"`
	CreatedAt           time.Time  `json:"created_at"`
	UpdatedAt           time.Time  `json:"updated_at"`
	SecretSet           bool       `json:"secret_set"`
	URLPath             string     `json:"url_path"`
}

type intakeEndpointCreatedDTO struct {
	intakeEndpointDTO
	Secret string `json:"secret"`
}

func toIntakeEndpointDTO(e gen.IntakeEndpoint) intakeEndpointDTO {
	return intakeEndpointDTO{
		ID: e.ID, Name: e.Name, RepoID: e.RepoID, WorkflowID: e.WorkflowID,
		TargetLabel: e.TargetLabel, Enabled: e.Enabled, TitleTemplate: e.TitleTemplate,
		DescriptionTemplate: e.DescriptionTemplate, DedupeKeyTemplate: e.DedupeKeyTemplate,
		LastReceivedAt: e.LastReceivedAt, CreatedAt: e.CreatedAt, UpdatedAt: e.UpdatedAt,
		SecretSet: e.Secret != "", URLPath: "/api/v1/intake/" + e.ID,
	}
}

func (h *IntakeEndpointsHandler) List(w http.ResponseWriter, r *http.Request) {
	eps, err := h.q.ListIntakeEndpoints(r.Context())
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]intakeEndpointDTO, 0, len(eps))
	for _, e := range eps {
		out = append(out, toIntakeEndpointDTO(e))
	}
	JSON(w, http.StatusOK, out)
}

func (h *IntakeEndpointsHandler) Get(w http.ResponseWriter, r *http.Request) {
	ep, err := h.q.GetIntakeEndpoint(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		Err(w, http.StatusNotFound, "intake endpoint not found")
		return
	}
	JSON(w, http.StatusOK, toIntakeEndpointDTO(ep))
}

type intakeEndpointBody struct {
	Name                string  `json:"name"`
	RepoID              string  `json:"repo_id"`
	WorkflowID          *string `json:"workflow_id"`
	TargetLabel         string  `json:"target_label"`
	Enabled             *bool   `json:"enabled"`
	TitleTemplate       string  `json:"title_template"`
	DescriptionTemplate string  `json:"description_template"`
	DedupeKeyTemplate   string  `json:"dedupe_key_template"`
}

// validate checks the body and returns the normalized workflow id (nil when
// empty). msg is non-empty on a 400; err on a lookup failure.
func (h *IntakeEndpointsHandler) validate(r *http.Request, b *intakeEndpointBody) (wfID *string, msg string, err error) {
	ctx := r.Context()
	b.Name = strings.TrimSpace(b.Name)
	if b.Name == "" {
		return nil, "name is required", nil
	}
	if strings.TrimSpace(b.TitleTemplate) == "" {
		return nil, "title_template is required", nil
	}
	for _, t := range []string{b.TitleTemplate, b.DescriptionTemplate, b.DedupeKeyTemplate} {
		if len(t) > intake.MaxTemplateLen {
			return nil, "templates must be at most " + strconv.Itoa(intake.MaxTemplateLen) + " bytes", nil
		}
	}
	repo, e := h.q.GetRepo(ctx, b.RepoID)
	if e != nil {
		if errors.Is(e, sql.ErrNoRows) {
			return nil, "repo not found", nil
		}
		return nil, "", e
	}
	effective := ""
	if b.WorkflowID != nil && *b.WorkflowID != "" {
		if _, e := h.q.GetWorkflow(ctx, *b.WorkflowID); e != nil {
			if errors.Is(e, sql.ErrNoRows) {
				return nil, "workflow not found", nil
			}
			return nil, "", e
		}
		effective = *b.WorkflowID
		wfID = b.WorkflowID
	} else if repo.WorkflowID != nil {
		effective = *repo.WorkflowID
	}
	if effective == "" {
		return nil, "repo has no workflow assigned; pick a workflow for the endpoint", nil
	}
	if b.TargetLabel != "" {
		labels, e := h.q.ListWorkflowLabels(ctx, effective)
		if e != nil {
			return nil, "", e
		}
		found := false
		for _, l := range labels {
			if l.Name == b.TargetLabel {
				found = true
				break
			}
		}
		if !found {
			return nil, "target_label is not a label in the endpoint's workflow", nil
		}
	}
	return wfID, "", nil
}

func newIntakeSecret() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return "whsec_" + hex.EncodeToString(buf), nil
}

func (h *IntakeEndpointsHandler) Create(w http.ResponseWriter, r *http.Request) {
	var body intakeEndpointBody
	if err := decode(r, &body); err != nil {
		Err(w, http.StatusBadRequest, "invalid request body")
		return
	}
	wfID, msg, err := h.validate(r, &body)
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	if msg != "" {
		Err(w, http.StatusBadRequest, msg)
		return
	}
	secret, err := newIntakeSecret()
	if err != nil {
		Err(w, http.StatusInternalServerError, "failed to generate secret")
		return
	}
	enabled := true
	if body.Enabled != nil {
		enabled = *body.Enabled
	}
	ep, err := h.q.CreateIntakeEndpoint(r.Context(), gen.CreateIntakeEndpointParams{
		ID: uuid.NewString(), Name: body.Name, RepoID: body.RepoID, WorkflowID: wfID,
		TargetLabel: body.TargetLabel, Secret: secret, Enabled: enabled,
		TitleTemplate: body.TitleTemplate, DescriptionTemplate: body.DescriptionTemplate,
		DedupeKeyTemplate: body.DedupeKeyTemplate,
	})
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	JSON(w, http.StatusCreated, intakeEndpointCreatedDTO{intakeEndpointDTO: toIntakeEndpointDTO(ep), Secret: secret})
}

func (h *IntakeEndpointsHandler) Update(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, err := h.q.GetIntakeEndpoint(r.Context(), id); err != nil {
		Err(w, http.StatusNotFound, "intake endpoint not found")
		return
	}
	var body intakeEndpointBody
	if err := decode(r, &body); err != nil {
		Err(w, http.StatusBadRequest, "invalid request body")
		return
	}
	wfID, msg, err := h.validate(r, &body)
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	if msg != "" {
		Err(w, http.StatusBadRequest, msg)
		return
	}
	enabled := true
	if body.Enabled != nil {
		enabled = *body.Enabled
	}
	ep, err := h.q.UpdateIntakeEndpoint(r.Context(), gen.UpdateIntakeEndpointParams{
		Name: body.Name, RepoID: body.RepoID, WorkflowID: wfID, TargetLabel: body.TargetLabel,
		Enabled: enabled, TitleTemplate: body.TitleTemplate, DescriptionTemplate: body.DescriptionTemplate,
		DedupeKeyTemplate: body.DedupeKeyTemplate, ID: id,
	})
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	JSON(w, http.StatusOK, toIntakeEndpointDTO(ep))
}

func (h *IntakeEndpointsHandler) Delete(w http.ResponseWriter, r *http.Request) {
	if err := h.q.DeleteIntakeEndpoint(r.Context(), chi.URLParam(r, "id")); err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *IntakeEndpointsHandler) RotateSecret(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, err := h.q.GetIntakeEndpoint(r.Context(), id); err != nil {
		Err(w, http.StatusNotFound, "intake endpoint not found")
		return
	}
	secret, err := newIntakeSecret()
	if err != nil {
		Err(w, http.StatusInternalServerError, "failed to generate secret")
		return
	}
	if err := h.q.UpdateIntakeEndpointSecret(r.Context(), gen.UpdateIntakeEndpointSecretParams{Secret: secret, ID: id}); err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	JSON(w, http.StatusOK, map[string]string{"secret": secret})
}

type intakeTestBody struct {
	Payload             map[string]any `json:"payload"`
	TitleTemplate       *string        `json:"title_template"`
	DescriptionTemplate *string        `json:"description_template"`
	DedupeKeyTemplate   *string        `json:"dedupe_key_template"`
}

// Test renders a sample payload against the endpoint (optionally with
// unsaved template overrides) and creates nothing.
func (h *IntakeEndpointsHandler) Test(w http.ResponseWriter, r *http.Request) {
	ep, err := h.q.GetIntakeEndpoint(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		Err(w, http.StatusNotFound, "intake endpoint not found")
		return
	}
	var body intakeTestBody
	if err := decode(r, &body); err != nil || body.Payload == nil {
		Err(w, http.StatusBadRequest, "payload (a JSON object) is required")
		return
	}
	if body.TitleTemplate != nil {
		ep.TitleTemplate = *body.TitleTemplate
	}
	if body.DescriptionTemplate != nil {
		ep.DescriptionTemplate = *body.DescriptionTemplate
	}
	if body.DedupeKeyTemplate != nil {
		ep.DedupeKeyTemplate = *body.DedupeKeyTemplate
	}
	// Re-parse through the same decoder as real deliveries so numbers match.
	raw, _ := json.Marshal(body.Payload)
	payload, err := intake.ParsePayload(raw)
	if err != nil {
		Err(w, http.StatusBadRequest, "invalid payload")
		return
	}
	sh, err := h.svc.Shape(r.Context(), ep, payload)
	if err != nil {
		if errors.Is(err, intakehook.ErrNoWorkflow) {
			Err(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	JSON(w, http.StatusOK, map[string]any{
		"title": sh.Title, "description": sh.Description, "dedupe_key": sh.DedupeKey,
		"label": sh.Label, "workflow_id": sh.WorkflowID, "priority": sh.Priority,
		"matched_rule_id": sh.MatchedRuleID, "existing_task_id": nilIfEmpty(sh.ExistingTaskID),
	})
}

func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// Receive handles POST /api/v1/intake/{endpoint_id}. It is mounted outside
// BearerAuth; the HMAC-SHA256 signature over the raw body is the
// authentication.
func (h *IntakeEndpointsHandler) Receive(w http.ResponseWriter, r *http.Request) {
	log := middleware.LoggerFromContext(r.Context()).With("component", "intake", "endpoint_id", chi.URLParam(r, "endpoint_id"))
	ep, err := h.q.GetIntakeEndpoint(r.Context(), chi.URLParam(r, "endpoint_id"))
	if err != nil {
		Err(w, http.StatusNotFound, "intake endpoint not found")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, intakeMaxBody))
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			Err(w, http.StatusRequestEntityTooLarge, "body exceeds 256 KB")
			return
		}
		Err(w, http.StatusBadRequest, "cannot read body")
		return
	}
	if !validWebhookSignature([]byte(ep.Secret), body, r.Header.Get(intakeSigHeader)) {
		log.Warn("intake: invalid or missing signature")
		Err(w, http.StatusUnauthorized, "invalid or missing signature")
		return
	}
	if !ep.Enabled {
		Err(w, http.StatusForbidden, "endpoint disabled")
		return
	}
	if ok, retry := h.limiter.allow(ep.ID); !ok {
		secs := int(retry.Seconds()) + 1
		w.Header().Set("Retry-After", strconv.Itoa(secs))
		Err(w, http.StatusTooManyRequests, "rate limit exceeded")
		return
	}
	payload, err := intake.ParsePayload(body)
	if err != nil {
		Err(w, http.StatusBadRequest, "invalid JSON payload")
		return
	}
	if _, isObj := payload.(map[string]any); !isObj {
		Err(w, http.StatusBadRequest, "payload must be a JSON object")
		return
	}
	res, err := h.svc.Receive(r.Context(), ep, payload)
	if err != nil {
		if errors.Is(err, intakehook.ErrNoWorkflow) {
			Err(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
		log.Error("intake: receive failed", "err", err)
		Err(w, http.StatusInternalServerError, "failed to process event")
		return
	}
	if res.Deduped {
		JSON(w, http.StatusOK, map[string]any{"task_id": res.TaskID, "deduped": true})
		return
	}
	JSON(w, http.StatusCreated, map[string]any{"task_id": res.TaskID, "created": true})
}

// SetClock overrides the rate limiter's clock (tests).
func (h *IntakeEndpointsHandler) SetClock(now func() time.Time) {
	h.limiter.mu.Lock()
	defer h.limiter.mu.Unlock()
	h.limiter.now = now
}
