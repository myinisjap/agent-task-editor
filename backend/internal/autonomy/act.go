package autonomy

import (
	"fmt"
	"strings"
	"time"
)

// PolicyActor is the actor id recorded in label history for moves the policy
// makes. API token names may not use it (see config).
const PolicyActor = "policy"

// UndoNote is the label-history note written when a person undoes an
// auto-approval. The policy recorder reads it to avoid re-approving.
const UndoNote = "Undo auto-approval"

// Decision outcomes stored on autonomy_decisions.outcome. 'ask' decisions
// have no outcome.
const (
	OutcomePending    = "pending"
	OutcomeApproved   = "approved"
	OutcomeHeld       = "held"
	OutcomeSuperseded = "superseded"
	OutcomeSkipped    = "skipped"
	OutcomeFailed     = "failed"
	OutcomeUndone     = "undone"
)

// EscapeSourceUndo is the autonomy_escapes.source for an undone approval.
const EscapeSourceUndo = "undo"

// ApprovalNote renders the label-history note for an auto-approval, e.g.
// "Auto-approved: gate set to Ask on flags; no flags; verify passed; held 10m".
// flags are the raised review-brief flag codes (none were in ask_on).
func ApprovalNote(mode Mode, flags []string, verifyStatus string, holdSeconds int) string {
	parts := make([]string, 0, 4)
	switch mode {
	case ModeOnFlags:
		parts = append(parts, "gate set to Ask on flags")
		if len(flags) == 0 {
			parts = append(parts, "no flags")
		} else {
			parts = append(parts, fmt.Sprintf("no blocking flags (%s)", strings.Join(flags, ", ")))
		}
	case ModeAuto:
		parts = append(parts, "gate set to Auto", "flags ignored")
	default:
		parts = append(parts, "gate set to "+string(mode))
	}
	if verifyStatus == "" {
		parts = append(parts, "no verify")
	} else {
		parts = append(parts, "verify passed")
	}
	if holdSeconds > 0 {
		parts = append(parts, "held "+FormatHold(holdSeconds))
	} else {
		parts = append(parts, "no hold")
	}
	return "Auto-approved: " + strings.Join(parts, "; ")
}

// Escape sources stored on autonomy_escapes.source.
const (
	EscapeSourceShouldHaveAsked = "should_have_asked"
	EscapeSourceRejected        = "rejected_later"
	EscapeSourceReopened        = "reopened"
)

// EscapeWindow is how long after a policy approval a reject or reopen still
// counts as an escape.
const EscapeWindow = 14 * 24 * time.Hour

// StepDownMode returns the next mode down (auto → on_flags → ask); false for ask.
func StepDownMode(m Mode) (Mode, bool) {
	switch m {
	case ModeAuto:
		return ModeOnFlags, true
	case ModeOnFlags:
		return ModeAsk, true
	}
	return "", false
}
