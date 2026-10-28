package handlers

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/myinisjap/agent-task-editor/backend/internal/autonomy"
	"github.com/myinisjap/agent-task-editor/backend/internal/needsme"
	"github.com/myinisjap/agent-task-editor/backend/internal/reviewbrief"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

// resolveLabelAutonomy validates a label's autonomy block. A nil block or mode
// ask yields the ask config with defaults (allowed on any label); any other
// mode requires isGate. Every error is prefixed with the label name.
func resolveLabelAutonomy(label string, in *autonomy.Config, isGate bool) (autonomy.Config, error) {
	ask := autonomy.Config{Mode: autonomy.ModeAsk, AskOn: []string{}, HoldSeconds: autonomy.DefaultHoldSeconds}
	if in == nil {
		return ask, nil
	}
	cfg := *in
	if cfg.Mode == "" {
		return ask, fmt.Errorf("label %q: autonomy.mode is required (ask|on_flags|auto)", label)
	}
	if err := cfg.Validate(reviewbrief.FlagCodes); err != nil {
		return ask, fmt.Errorf("label %q: %w", label, err)
	}
	if cfg.AskOn == nil {
		cfg.AskOn = []string{}
	}
	if cfg.Mode == autonomy.ModeAsk {
		return ask, nil
	}
	if !isGate {
		return ask, fmt.Errorf("label %q: autonomy is only allowed on human-gate labels (every transition out of it must be human-triggered, it needs at least one, and it must not be agent_ignore or terminal)", label)
	}
	return cfg, nil
}

// gateInput is the minimal per-label/transition shape needed for the gate rule.
type autoGateLabel struct {
	Name        string
	AgentIgnore bool
	IsTerminal  bool
}

type autoGateTransition struct{ From, To, Trigger string }

// gateSet returns which of the submitted labels are human gates, using the
// same rule the Inbox human_gate item uses.
func gateSet(labels []autoGateLabel, transitions []autoGateTransition) map[string]bool {
	def := needsme.WorkflowDef{}
	for _, l := range labels {
		gl := gen.WorkflowLabel{Name: l.Name}
		if l.AgentIgnore {
			gl.AgentIgnore = 1
		}
		if l.IsTerminal {
			gl.IsTerminal = 1
		}
		def.Labels = append(def.Labels, gl)
	}
	for _, t := range transitions {
		def.Transitions = append(def.Transitions, gen.WorkflowTransition{FromLabel: t.From, ToLabel: t.To, TriggerType: t.Trigger})
	}
	out := map[string]bool{}
	for _, l := range labels {
		out[l.Name] = needsme.IsHumanGate(def, l.Name)
	}
	return out
}

// saveLabelAutonomy persists a non-ask config on a freshly created label.
// Ask configs are the column defaults, so nothing is written.
func saveLabelAutonomy(ctx context.Context, q *gen.Queries, labelID string, cfg autonomy.Config) error {
	if cfg.Mode == autonomy.ModeAsk || cfg.Mode == "" {
		return nil
	}
	askOn, err := json.Marshal(append([]string{}, cfg.AskOn...))
	if err != nil {
		return err
	}
	return q.SetWorkflowLabelAutonomy(ctx, gen.SetWorkflowLabelAutonomyParams{
		AutonomyMode:        string(cfg.Mode),
		AutonomyAskOn:       string(askOn),
		AutonomyHoldSeconds: int64(cfg.HoldSeconds),
		AutonomyMaxCostUsd:  cfg.MaxCostUSD,
		ID:                  labelID,
	})
}

// labelAutonomyJSON is the wire shape of a label's non-ask autonomy policy.
type labelAutonomyJSON struct {
	Mode        string   `json:"mode"`
	AskOn       []string `json:"ask_on"`
	HoldSeconds int64    `json:"hold_seconds"`
	MaxCostUSD  *float64 `json:"max_cost_usd"`
}

// autonomyJSON converts a stored label to its wire form (nil when mode ask).
func autonomyJSON(l gen.WorkflowLabel) *labelAutonomyJSON {
	if l.AutonomyMode == string(autonomy.ModeAsk) || l.AutonomyMode == "" {
		return nil
	}
	askOn := []string{}
	_ = json.Unmarshal([]byte(l.AutonomyAskOn), &askOn)
	if askOn == nil {
		askOn = []string{}
	}
	return &labelAutonomyJSON{Mode: l.AutonomyMode, AskOn: askOn, HoldSeconds: l.AutonomyHoldSeconds, MaxCostUSD: l.AutonomyMaxCostUsd}
}

// workflowLabelResponse is gen.WorkflowLabel plus a decoded autonomy object.
// The raw autonomy_* columns are shadowed with nil pointers + omitempty so they
// drop out of the JSON (same trick as taskResponse).
type workflowLabelResponse struct {
	gen.WorkflowLabel
	AutonomyModeRaw    *struct{}          `json:"autonomy_mode,omitempty"`
	AutonomyAskOnRaw   *struct{}          `json:"autonomy_ask_on,omitempty"`
	AutonomyHoldRaw    *struct{}          `json:"autonomy_hold_seconds,omitempty"`
	AutonomyMaxCostRaw *struct{}          `json:"autonomy_max_cost_usd,omitempty"`
	Autonomy           *labelAutonomyJSON `json:"autonomy"`
}

// yamlAutonomy is the YAML autonomy block on a label.
type yamlAutonomy struct {
	Mode       string   `yaml:"mode"`
	AskOn      []string `yaml:"ask_on,omitempty"`
	Hold       string   `yaml:"hold,omitempty"`
	MaxCostUSD *float64 `yaml:"max_cost_usd,omitempty"`
}

// resolveYAMLAutonomy converts and validates a YAML label's autonomy block.
func resolveYAMLAutonomy(l yamlLabel, isGate bool) (autonomy.Config, error) {
	if l.Autonomy == nil {
		return resolveLabelAutonomy(l.Name, nil, isGate)
	}
	cfg := autonomy.Config{Mode: autonomy.Mode(l.Autonomy.Mode), AskOn: l.Autonomy.AskOn, HoldSeconds: autonomy.DefaultHoldSeconds, MaxCostUSD: l.Autonomy.MaxCostUSD}
	if l.Autonomy.Hold != "" {
		sec, err := autonomy.ParseHold(l.Autonomy.Hold)
		if err != nil {
			return cfg, fmt.Errorf("label %q: autonomy.%w", l.Name, err)
		}
		cfg.HoldSeconds = sec
	}
	return resolveLabelAutonomy(l.Name, &cfg, isGate)
}

// yamlGateSet computes gate-ness over a submitted YAML workflow.
func (wf yamlWorkflow) gateSet() map[string]bool {
	var ls []autoGateLabel
	for _, l := range wf.Labels {
		ls = append(ls, autoGateLabel{l.Name, l.AgentIgnore, l.IsTerminal})
	}
	var ts []autoGateTransition
	for _, t := range wf.Transitions {
		ts = append(ts, autoGateTransition{t.From, t.To, t.TriggerType})
	}
	return gateSet(ls, ts)
}
