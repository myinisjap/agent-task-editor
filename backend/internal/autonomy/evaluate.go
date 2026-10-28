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
}

// Reason explains part of a decision.
type Reason struct {
	Code   string `json:"code"`
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
	if in.AlwaysAsk {
		reasons = append(reasons, Reason{ReasonAlwaysAsk, "task is marked always_ask"})
	}
	if in.VerifyStatus != "" && in.VerifyStatus != verifyPass {
		reasons = append(reasons, Reason{ReasonVerifyFailed, "verification status: " + in.VerifyStatus})
	}
	if in.MaxCostUSD != nil && in.CostUSD > *in.MaxCostUSD {
		reasons = append(reasons, Reason{ReasonCostExceeded, fmt.Sprintf("$%.2f > $%.2f", in.CostUSD, *in.MaxCostUSD)})
	}
	if in.Mode == ModeOnFlags {
		if !in.HasDiff {
			reasons = append(reasons, Reason{ReasonNoDiff, "no diff against base; flags cannot be computed"})
		} else {
			raised := map[string]bool{}
			for _, f := range in.Flags {
				raised[f] = true
			}
			for _, code := range in.AskOn {
				if raised[code] {
					reasons = append(reasons, Reason{ReasonFlagRaised, code})
				}
			}
		}
	}
	if len(reasons) > 0 {
		return Decision{Decision: DecisionAsk, Reasons: reasons}
	}
	if in.Mode == ModeAuto {
		return Decision{Decision: DecisionApprove, Reasons: []Reason{{ReasonAutoPolicy, "auto mode and no hard stops"}}}
	}
	return Decision{Decision: DecisionApprove, Reasons: []Reason{{ReasonNoBlockingFlags, "no ask_on flag raised"}}}
}
