package agent

import (
	"errors"
	"testing"
)

func TestErrTransient_ErrorAndUnwrap(t *testing.T) {
	cause := errors.New("connection reset")
	err := &ErrTransient{Cause: cause}

	if got, want := err.Error(), "transient error: connection reset"; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
	if !err.Transient() {
		t.Errorf("Transient() = false, want true")
	}
	if got := err.Unwrap(); got != cause {
		t.Errorf("Unwrap() = %v, want %v", got, cause)
	}
	if !errors.Is(err, cause) {
		t.Errorf("errors.Is(err, cause) = false, want true (Unwrap should chain)")
	}

	// Verify the transientErr marker interface is satisfied, since pool.go
	// discriminates transient vs. non-retryable errors via errors.As on this
	// unexported interface.
	var te transientErr
	if !errors.As(err, &te) {
		t.Fatalf("expected *ErrTransient to satisfy transientErr")
	}
	if !te.Transient() {
		t.Errorf("transientErr.Transient() = false, want true")
	}
}

func TestErrMaxTurns_Error(t *testing.T) {
	err := &ErrMaxTurns{MaxTurns: 12}
	if got, want := err.Error(), "exceeded max turns (12)"; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}

	// ErrMaxTurns must NOT satisfy transientErr — turn exhaustion should not
	// consume the transient-retry budget (see doc comment on the type).
	var te transientErr
	if errors.As(err, &te) {
		t.Errorf("ErrMaxTurns must not satisfy transientErr, but errors.As succeeded")
	}
}

func TestErrCostBudgetExceeded_Error(t *testing.T) {
	err := &ErrCostBudgetExceeded{SpentUSD: 12.345, BudgetUSD: 10}
	if got, want := err.Error(), "mid-run cost budget exceeded: $12.35 of $10.00"; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}

	// Must NOT satisfy transientErr either — a budget stop is a policy
	// decision, not a retryable infra blip (see doc comment on the type).
	var te transientErr
	if errors.As(err, &te) {
		t.Errorf("ErrCostBudgetExceeded must not satisfy transientErr, but errors.As succeeded")
	}
}
