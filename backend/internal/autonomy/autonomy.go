// Package autonomy holds the pure policy types and evaluator for the
// per-gate autonomy dial. It is a leaf package (stdlib and pathglob only).
package autonomy

import (
	"fmt"
	"strings"
	"time"
)

// Mode is a gate's autonomy setting.
type Mode string

const (
	ModeAsk     Mode = "ask"
	ModeOnFlags Mode = "on_flags"
	ModeAuto    Mode = "auto"
)

// DefaultHoldSeconds is the default hold window before an auto-approval.
const DefaultHoldSeconds = 600

// ParseMode validates a mode string.
func ParseMode(s string) (Mode, error) {
	switch Mode(s) {
	case ModeAsk, ModeOnFlags, ModeAuto:
		return Mode(s), nil
	}
	return "", fmt.Errorf("invalid autonomy mode %q (want ask, on_flags or auto)", s)
}

// Config is the autonomy policy attached to a workflow label.
type Config struct {
	Mode        Mode
	AskOn       []string
	HoldSeconds int
	MaxCostUSD  *float64
}

// Validate checks the config and normalises AskOn (deduplicated, order kept).
func (c *Config) Validate(knownFlags []string) error {
	if _, err := ParseMode(string(c.Mode)); err != nil {
		return err
	}
	known := make(map[string]bool, len(knownFlags))
	for _, f := range knownFlags {
		known[f] = true
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(c.AskOn))
	for _, code := range c.AskOn {
		if !known[code] {
			return fmt.Errorf("unknown ask_on flag %q (valid: %s)", code, strings.Join(knownFlags, ", "))
		}
		if !seen[code] {
			seen[code] = true
			out = append(out, code)
		}
	}
	c.AskOn = out
	if c.HoldSeconds < 0 {
		return fmt.Errorf("hold must not be negative")
	}
	if c.MaxCostUSD != nil && *c.MaxCostUSD < 0 {
		return fmt.Errorf("max_cost_usd must not be negative")
	}
	return nil
}

// ParseHold parses a duration like "10m" into whole seconds.
func ParseHold(s string) (int, error) {
	d, err := time.ParseDuration(strings.TrimSpace(s))
	if err != nil {
		return 0, fmt.Errorf("invalid hold %q: %w", s, err)
	}
	if d < 0 {
		return 0, fmt.Errorf("hold must not be negative")
	}
	if d%time.Second != 0 {
		return 0, fmt.Errorf("hold %q must be a whole number of seconds", s)
	}
	return int(d / time.Second), nil
}

// FormatHold renders seconds as the largest exact unit (h, m or s).
func FormatHold(sec int) string {
	switch {
	case sec != 0 && sec%3600 == 0:
		return fmt.Sprintf("%dh", sec/3600)
	case sec != 0 && sec%60 == 0:
		return fmt.Sprintf("%dm", sec/60)
	}
	return fmt.Sprintf("%ds", sec)
}
