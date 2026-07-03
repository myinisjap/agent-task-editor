package agent

import "fmt"

// ErrTransient marks an error as a transient infrastructure problem (network
// blip, upstream 5xx, timeout) rather than a genuine task failure. The pool
// uses this to decide whether to auto-retry the run against the task's
// configured retry budget, rather than treating it as an outright failure
// that requires human attention.
type ErrTransient struct {
	Cause error
}

func (e *ErrTransient) Error() string {
	return fmt.Sprintf("transient error: %v", e.Cause)
}

func (e *ErrTransient) Unwrap() error {
	return e.Cause
}

// Transient implements the transientErr marker interface.
func (e *ErrTransient) Transient() bool { return true }

// transientErr is implemented by errors that represent a transient
// infrastructure problem (as opposed to a genuine, non-retryable task
// failure). Both ErrRateLimit and ErrTransient satisfy this so pool.go can
// classify either with a single errors.As check.
type transientErr interface {
	Transient() bool
}
