package handlers

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/myinisjap/agent-task-editor/backend/internal/cronexpr"
	"github.com/myinisjap/agent-task-editor/backend/internal/intake"
	"github.com/myinisjap/agent-task-editor/backend/internal/recipes"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

// RecipesHandler lists the bundled automation recipes and installs/removes
// them. A recipe expands into ordinary templates/schedules/endpoints/rules
// tagged with recipe_id; nothing here changes how agents run.
type RecipesHandler struct {
	db *sql.DB
	q  *gen.Queries

	// afterStep, when set (tests), is called after each install step with the
	// step name; a non-nil error aborts and rolls back the install.
	afterStep func(step string) error
}

func NewRecipesHandler(db *sql.DB, q *gen.Queries) *RecipesHandler {
	return &RecipesHandler{db: db, q: q}
}

type recipeObjects struct {
	TemplateID *string `json:"template_id"`
	ScheduleID *string `json:"schedule_id"`
	EndpointID *string `json:"endpoint_id"`
	RuleID     *string `json:"rule_id"`
	RepoID     *string `json:"repo_id"`
}

type recipeView struct {
	recipes.Recipe
	Installed bool          `json:"installed"`
	Objects   recipeObjects `json:"objects"`
}

func (h *RecipesHandler) objectsFor(ctx context.Context, q *gen.Queries, id string) (recipeObjects, bool, error) {
	var o recipeObjects
	found := false
	tpls, err := q.ListTaskTemplates(ctx)
	if err != nil {
		return o, false, err
	}
	for _, t := range tpls {
		if t.RecipeID == id {
			v := t.ID
			o.TemplateID, found = &v, true
		}
	}
	scheds, err := q.ListTaskSchedules(ctx)
	if err != nil {
		return o, false, err
	}
	for _, s := range scheds {
		if s.RecipeID == id {
			v, rp := s.ID, s.RepoID
			o.ScheduleID, o.RepoID, found = &v, &rp, true
		}
	}
	eps, err := q.ListIntakeEndpointsByRecipe(ctx, id)
	if err != nil {
		return o, false, err
	}
	for _, e := range eps {
		v, rp := e.ID, e.RepoID
		o.EndpointID, o.RepoID, found = &v, &rp, true
	}
	rules, err := q.ListIntakeRules(ctx)
	if err != nil {
		return o, false, err
	}
	for _, r := range rules {
		if r.RecipeID == id {
			v := r.ID
			o.RuleID, found = &v, true
		}
	}
	return o, found, nil
}

// List returns every recipe with its parameters and installed state.
func (h *RecipesHandler) List(w http.ResponseWriter, r *http.Request) {
	out := []recipeView{}
	for _, rc := range recipes.All() {
		o, installed, err := h.objectsFor(r.Context(), h.q, rc.ID)
		if err != nil {
			Err(w, http.StatusInternalServerError, err.Error())
			return
		}
		out = append(out, recipeView{Recipe: rc, Installed: installed, Objects: o})
	}
	JSON(w, http.StatusOK, out)
}

type recipeInstallBody struct {
	Params map[string]string `json:"params"`
	// ConfirmAutoStart acknowledges that a non-empty target_label which is not
	// a human-review label lets tasks start agents without a human promoting
	// them.
	ConfirmAutoStart bool `json:"confirm_auto_start"`
}

type recipeInstallResult struct {
	Recipe      string                 `json:"recipe"`
	Template    gen.TaskTemplate       `json:"template"`
	Schedule    *gen.TaskSchedule      `json:"schedule,omitempty"`
	Endpoint    *intakeEndpointDTO     `json:"endpoint,omitempty"`
	Rule        *intakeRuleResponse    `json:"rule,omitempty"`
	URL         string                 `json:"url,omitempty"`
	Secret      string                 `json:"secret,omitempty"`
	Snippet     string                 `json:"snippet,omitempty"`
	TargetLabel string                 `json:"target_label"`
	Warnings    []string               `json:"warnings"`
	Extra       map[string]interface{} `json:"-"`
}

var branchRe = regexp.MustCompile(`^[A-Za-z0-9._/-]{1,100}$`)

type installError struct {
	status int
	msg    string
	code   string
}

func (e *installError) Error() string { return e.msg }

func badReq(format string, a ...any) *installError {
	return &installError{status: http.StatusBadRequest, msg: fmt.Sprintf(format, a...)}
}

func requestBaseURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if p := r.Header.Get("X-Forwarded-Proto"); p == "https" || p == "http" {
		scheme = p
	}
	host := r.Host
	if f := r.Header.Get("X-Forwarded-Host"); f != "" {
		host = f
	}
	return scheme + "://" + host
}

// Install creates the recipe's objects in one transaction.
func (h *RecipesHandler) Install(w http.ResponseWriter, r *http.Request) {
	rc, ok := recipes.Get(chi.URLParam(r, "id"))
	if !ok {
		Err(w, http.StatusNotFound, "recipe not found")
		return
	}
	var body recipeInstallBody
	if err := decode(r, &body); err != nil {
		Err(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if body.Params == nil {
		body.Params = map[string]string{}
	}
	res, err := h.install(r.Context(), rc, body, requestBaseURL(r))
	if err != nil {
		var ie *installError
		if errors.As(err, &ie) {
			if ie.code != "" {
				JSON(w, ie.status, map[string]string{"error": ie.msg, "code": ie.code})
				return
			}
			Err(w, ie.status, ie.msg)
			return
		}
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	JSON(w, http.StatusCreated, res)
}

func (h *RecipesHandler) step(name string) error {
	if h.afterStep != nil {
		return h.afterStep(name)
	}
	return nil
}

func (h *RecipesHandler) install(ctx context.Context, rc recipes.Recipe, body recipeInstallBody, baseURL string) (*recipeInstallResult, error) {
	p := body.Params
	for k, v := range p {
		p[k] = strings.TrimSpace(v)
	}
	if _, installed, err := h.objectsFor(ctx, h.q, rc.ID); err != nil {
		return nil, err
	} else if installed {
		return nil, &installError{status: http.StatusConflict, msg: "recipe is already installed; remove it first"}
	}

	if p["repo_id"] == "" {
		return nil, badReq("repo_id is required")
	}
	repo, err := h.q.GetRepo(ctx, p["repo_id"])
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, badReq("repo not found")
		}
		return nil, err
	}
	wfID := p["workflow_id"]
	if wfID != "" {
		if _, err := h.q.GetWorkflow(ctx, wfID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, badReq("workflow not found")
			}
			return nil, err
		}
	} else if repo.WorkflowID != nil {
		wfID = *repo.WorkflowID
	}
	if wfID == "" {
		return nil, badReq("repo has no workflow assigned; pick a workflow")
	}
	labels, err := h.q.ListWorkflowLabels(ctx, wfID)
	if err != nil {
		return nil, err
	}
	gate, first := gateLabel(labels)
	if gate == "" {
		gate = first
	}

	var warnings []string
	target := p["target_label"]
	if target != "" {
		found, isGate := false, false
		for _, l := range labels {
			if l.Name == target {
				found, isGate = true, l.AgentIgnore != 0
				break
			}
		}
		if !found {
			return nil, badReq("target_label is not a label in the workflow")
		}
		if !isGate {
			msg := "target_label \"" + target + "\" is agent-triggerable: tasks created by this recipe will start an agent automatically without a human promoting them. Leave it empty to park tasks for review."
			if !body.ConfirmAutoStart {
				return nil, &installError{status: http.StatusBadRequest, msg: msg, code: "auto_start_confirmation_required"}
			}
			warnings = append(warnings, msg)
		}
	}
	if b := p["branch"]; b != "" && !branchRe.MatchString(b) {
		return nil, badReq("branch contains invalid characters")
	}

	cronExpr := ""
	if rc.Kind == "schedule" {
		cronExpr = p["cron_expr"]
		if cronExpr == "" {
			cronExpr = rc.DefaultCron
		}
		if _, err := cronexpr.Parse(cronExpr); err != nil {
			return nil, badReq("invalid cron_expr: %s", err.Error())
		}
	}

	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	q := h.q.WithTx(tx)

	res := &recipeInstallResult{Recipe: rc.ID, TargetLabel: target, Warnings: warnings}
	if res.Warnings == nil {
		res.Warnings = []string{}
	}

	tpl, err := q.CreateTaskTemplate(ctx, gen.CreateTaskTemplateParams{
		ID: uuid.NewString(), Name: rc.Template.Name, Title: rc.Template.Title,
		Description: rc.Template.Description, Type: rc.Template.Type,
	})
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return nil, &installError{status: http.StatusConflict, msg: "a template named \"" + rc.Template.Name + "\" already exists; rename or delete it first"}
		}
		return nil, err
	}
	if err := q.SetTaskTemplateRecipe(ctx, gen.SetTaskTemplateRecipeParams{RecipeID: rc.ID, ID: tpl.ID}); err != nil {
		return nil, err
	}
	tpl.RecipeID = rc.ID
	res.Template = tpl
	if err := h.step("template"); err != nil {
		return nil, err
	}

	switch rc.Kind {
	case "schedule":
		// Same default as the schedules API: empty = the human-gate label.
		label := target
		if label == "" {
			label = gate
		}
		res.TargetLabel = label
		sched, err := q.CreateTaskSchedule(ctx, gen.CreateTaskScheduleParams{
			ID: uuid.NewString(), TemplateID: tpl.ID, RepoID: repo.ID,
			CronExpr: cronExpr, TargetLabel: label, Enabled: true,
		})
		if err != nil {
			return nil, err
		}
		if err := q.SetTaskScheduleRecipe(ctx, gen.SetTaskScheduleRecipeParams{RecipeID: rc.ID, ID: sched.ID}); err != nil {
			return nil, err
		}
		sched.RecipeID = rc.ID
		res.Schedule = &sched
		if err := h.step("schedule"); err != nil {
			return nil, err
		}
	case "webhook":
		ed := rc.Endpoint
		secret, err := newIntakeSecret()
		if err != nil {
			return nil, err
		}
		var epWF *string
		if p["workflow_id"] != "" {
			v := p["workflow_id"]
			epWF = &v
		}
		ep, err := q.CreateIntakeEndpoint(ctx, gen.CreateIntakeEndpointParams{
			ID: uuid.NewString(), Name: ed.Name, RepoID: repo.ID, WorkflowID: epWF,
			TargetLabel: target, Secret: secret, Enabled: true,
			TitleTemplate: ed.TitleTemplate, DescriptionTemplate: ed.DescriptionTemplate,
			DedupeKeyTemplate: ed.DedupeKeyTemplate,
		})
		if err != nil {
			return nil, err
		}
		if err := q.SetIntakeEndpointRecipe(ctx, gen.SetIntakeEndpointRecipeParams{RecipeID: rc.ID, ID: ep.ID}); err != nil {
			return nil, err
		}
		ep.RecipeID = rc.ID
		dto := toIntakeEndpointDTO(ep)
		res.Endpoint = &dto
		if err := h.step("endpoint"); err != nil {
			return nil, err
		}

		if err := intake.ValidatePattern(ed.RuleTitlePattern); err != nil {
			return nil, err
		}
		repoID := repo.ID
		tplID := tpl.ID
		rule, err := q.CreateIntakeRule(ctx, gen.CreateIntakeRuleParams{
			ID: uuid.NewString(), Name: ed.RuleName, Enabled: true,
			MatchSource: "webhook", MatchRepoID: &repoID, MatchLabels: "[]",
			MatchTitlePattern: ed.RuleTitlePattern, MatchAuthorAssoc: "[]",
			ApplyTemplateID: &tplID,
		})
		if err != nil {
			return nil, err
		}
		if err := q.SetIntakeRuleRecipe(ctx, gen.SetIntakeRuleRecipeParams{RecipeID: rc.ID, ID: rule.ID}); err != nil {
			return nil, err
		}
		rule.RecipeID = rc.ID
		rr := toIntakeRuleResponse(rule)
		res.Rule = &rr
		if err := h.step("rule"); err != nil {
			return nil, err
		}

		res.URL = baseURL + dto.URLPath
		res.Secret = secret
		res.Snippet = recipes.Snippet(ed.Sender, res.URL, p)
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return res, nil
}

// Remove deletes exactly the objects tagged with the recipe id, in one
// transaction. Tasks already created are left alone.
func (h *RecipesHandler) Remove(w http.ResponseWriter, r *http.Request) {
	rc, ok := recipes.Get(chi.URLParam(r, "id"))
	if !ok {
		Err(w, http.StatusNotFound, "recipe not found")
		return
	}
	ctx := r.Context()
	if _, installed, err := h.objectsFor(ctx, h.q, rc.ID); err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	} else if !installed {
		Err(w, http.StatusNotFound, "recipe is not installed")
		return
	}
	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer func() { _ = tx.Rollback() }()
	q := h.q.WithTx(tx)
	for _, f := range []func(context.Context, string) error{
		q.DeleteIntakeRulesByRecipe, q.DeleteTaskSchedulesByRecipe,
		q.DeleteIntakeEndpointsByRecipe, q.DeleteTaskTemplatesByRecipe,
	} {
		if err := f(ctx, rc.ID); err != nil {
			Err(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	if err := tx.Commit(); err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// SetAfterStep installs a hook called after each install step (tests): a
// non-nil error aborts and rolls back the whole install.
func (h *RecipesHandler) SetAfterStep(f func(step string) error) { h.afterStep = f }
