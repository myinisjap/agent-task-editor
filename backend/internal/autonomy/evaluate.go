package autonomy

import "fmt"

// Reason codes are part of the contract for later slices.
const (
	ReasonModeAsk         = "mode_ask"
	ReasonAlwaysAsk       = "always_ask"
	ReasonVerifyFailed    = "verify_failed"
	ReasonCostExceeded    = "cost_exceeded"
	ReasonNoDiff          = "no_diff"
	ReasonFlagRaised      = "flag_raised"
	ReasonAutoPolicy      = "auto_policy"
	ReasonNoBlockingFlags = "no_blocking_flags"
	ReasonUndone          = "undone"
	DecisionApprove       = "approve"
	DecisionAsk           = "ask"
	verifyPass            = "pass"
)

// Input is everything Evaluate needs; the caller gathers it.
type Input struct {
	Mode         Mode
	AskOn        []string
	Flags        []string // raised review-brief flag codes
	HasDiff      bool
	VerifyStatus string // "" (none) | pass | fail | timeout | error
	CostUSD      float64
	MaxCostUSD   *float64
	AlwaysAsk    bool
	// Undone is set when the task is landing on this gate because a person
	// undid an auto-approval; the policy must not re-approve it.
	Undone bool
	// Rules are the ask rules applicable to this gate and task; Facts are what
	// they are matched against.
	Rules []AskRule
	Facts Facts
}

// Reason explains part of a decision.
type Reason struct {
	Code   string `json:"code"`
	RuleID string `json:"rule_id,omitempty"`
	Detail string `json:"detail"`
}

// Decision is the evaluator's verdict.
type Decision struct {
	Decision string   `json:"decision"`
	Reasons  []Reason `json:"reasons"`
}

// Evaluate applies the gate policy. Pure; never returns nil Reasons.
func Evaluate(in Input) Decision {
	if in.Mode != ModeOnFlags && in.Mode != ModeAuto {
		return Decision{Decision: DecisionAsk, Reasons: []Reason{{Code: ReasonModeAsk, Detail: "gate mode is ask"}}}
	}
	reasons := []Reason{}
	if in.Undone {
		reasons = append(reasons, Reason{Code: ReasonUndone, Detail: "auto-approval was undone; a person decides"})
	}
	if in.AlwaysAsk {
		reasons = append(reasons, Reason{Code: ReasonAlwaysAsk, Detail: "task is marked always_ask"})
	}
	if in.VerifyStatus != "" && in.VerifyStatus != verifyPass {
		reasons = append(reasons, Reason{Code: ReasonVerifyFailed, Detail: "verification status: " + in.VerifyStatus})
	}
	if in.MaxCostUSD != nil && in.CostUSD > *in.MaxCostUSD {
		reasons = append(reasons, Reason{Code: ReasonCostExceeded, Detail: fmt.Sprintf("$%.2f > $%.2f", in.CostUSD, *in.MaxCostUSD)})
	}
	for _, r := range MatchRules(in.Rules, in.Facts) {
		reasons = append(reasons, Reason{Code: ReasonAskRule, Detail: r.Summary(), RuleID: r.ID})
	}
	if in.Mode == ModeOnFlags {
		if !in.HasDiff {
			reasons = append(reasons, Reason{Code: ReasonNoDiff, Detail: "no diff against base; flags cannot be computed"})
		} else {
			raised := map[string]bool{}
			for _, f := range in.Flags {
				raised[f] = true
			}
			for _, code := range in.AskOn {
				if raised[code] {
					reasons = append(reasons, Reason{Code: ReasonFlagRaised, Detail: code})
				}
			}
		}
	}
	if len(reasons) > 0 {
		return Decision{Decision: DecisionAsk, Reasons: reasons}
	}
	if in.Mode == ModeAuto {
		return Decision{Decision: DecisionApprove, Reasons: []Reason{{Code: ReasonAutoPolicy, Detail: "auto mode and no hard stops"}}}
	}
	return Decision{Decision: DecisionApprove, Reasons: []Reason{{Code: ReasonNoBlockingFlags, Detail: "no ask_on flag raised"}}}
}
