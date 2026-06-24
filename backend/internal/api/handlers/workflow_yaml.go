package handlers

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
	"gopkg.in/yaml.v3"
)

// yamlWorkflow is the portable YAML representation of a workflow.
type yamlWorkflow struct {
	Name        string            `yaml:"name"`
	Description string            `yaml:"description,omitempty"`
	Labels      []yamlLabel       `yaml:"labels"`
	Transitions []yamlTransition  `yaml:"transitions"`
}

type yamlLabel struct {
	Name        string `yaml:"name"`
	Color       string `yaml:"color"`
	SortOrder   int    `yaml:"sort_order"`
	AgentIgnore bool   `yaml:"agent_ignore,omitempty"`
	IsTerminal  bool   `yaml:"is_terminal,omitempty"`
}

type yamlTransition struct {
	From        string  `yaml:"from"`
	To          string  `yaml:"to"`
	TriggerType string  `yaml:"trigger"`
	AgentConfig *string `yaml:"agent_config,omitempty"`
}

// ExportWorkflowYAML exports a workflow as YAML.
func (h *WorkflowsHandler) ExportWorkflowYAML(w http.ResponseWriter, r *http.Request) {
	wf, err := h.q.GetWorkflow(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		Err(w, http.StatusNotFound, "workflow not found")
		return
	}
	labels, err := h.q.ListWorkflowLabels(r.Context(), wf.ID)
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	transitions, err := h.q.ListWorkflowTransitions(r.Context(), wf.ID)
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}

	out := yamlWorkflow{Name: wf.Name, Description: wf.Description}
	for _, l := range labels {
		out.Labels = append(out.Labels, yamlLabel{
			Name:        l.Name,
			Color:       l.Color,
			SortOrder:   int(l.SortOrder),
			AgentIgnore: l.AgentIgnore != 0,
			IsTerminal:  l.IsTerminal != 0,
		})
	}
	for _, t := range transitions {
		out.Transitions = append(out.Transitions, yamlTransition{
			From:        t.FromLabel,
			To:          t.ToLabel,
			TriggerType: t.TriggerType,
			AgentConfig: t.AgentConfigID,
		})
	}

	data, err := yaml.Marshal(out)
	if err != nil {
		Err(w, http.StatusInternalServerError, "yaml marshal error")
		return
	}
	w.Header().Set("Content-Type", "application/yaml")
	w.Header().Set("Content-Disposition", `attachment; filename="workflow.yaml"`)
	w.Write(data)
}

// ImportWorkflowYAML creates a new workflow from an uploaded YAML body.
func (h *WorkflowsHandler) ImportWorkflowYAML(w http.ResponseWriter, r *http.Request) {
	var in yamlWorkflow
	if err := yaml.NewDecoder(r.Body).Decode(&in); err != nil {
		Err(w, http.StatusBadRequest, "invalid yaml: "+err.Error())
		return
	}
	if in.Name == "" {
		Err(w, http.StatusBadRequest, "name is required")
		return
	}

	ctx := r.Context()
	wfID := uuid.NewString()
	wf, err := h.q.CreateWorkflow(ctx, gen.CreateWorkflowParams{
		ID:          wfID,
		Name:        in.Name,
		Description: in.Description,
	})
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}

	for _, l := range in.Labels {
		agentIgnore := int64(0)
		if l.AgentIgnore {
			agentIgnore = 1
		}
		isTerminal := int64(0)
		if l.IsTerminal {
			isTerminal = 1
		}
		if _, err := h.q.CreateWorkflowLabel(ctx, gen.CreateWorkflowLabelParams{
			ID:          uuid.NewString(),
			WorkflowID:  wfID,
			Name:        l.Name,
			Color:       l.Color,
			SortOrder:   int64(l.SortOrder),
			AgentIgnore: agentIgnore,
			IsTerminal:  isTerminal,
		}); err != nil {
			Err(w, http.StatusInternalServerError, err.Error())
			return
		}
	}

	for _, t := range in.Transitions {
		if _, err := h.q.CreateWorkflowTransition(ctx, gen.CreateWorkflowTransitionParams{
			ID:            uuid.NewString(),
			WorkflowID:    wfID,
			FromLabel:     t.From,
			ToLabel:       t.To,
			TriggerType:   t.TriggerType,
			AgentConfigID: t.AgentConfig,
		}); err != nil {
			Err(w, http.StatusInternalServerError, err.Error())
			return
		}
	}

	JSON(w, http.StatusCreated, wf)
}
