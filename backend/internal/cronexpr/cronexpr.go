// Package cronexpr implements a minimal, dependency-free evaluator for the
// standard 5-field cron format: "minute hour day-of-month month day-of-week".
// It supports the subset needed by task schedule presets (hourly, daily,
// weekly-on-day-at-time) plus raw cron entry: '*', single numeric values,
// comma-separated lists, and '*/N' step values. It does not support ranges
// ("1-5"), named months/weekdays, or special characters like L/W/#.
package cronexpr

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// field bounds, in order: minute, hour, day-of-month, month, day-of-week.
var fieldBounds = [5][2]int{
	{0, 59}, // minute
	{0, 23}, // hour
	{1, 31}, // day of month
	{1, 12}, // month
	{0, 6},  // day of week (0 = Sunday)
}

var fieldNames = [5]string{"minute", "hour", "day-of-month", "month", "day-of-week"}

// Schedule is a parsed cron expression that can compute its next fire time.
type Schedule struct {
	// minute, hour, dayOfMonth, month, dayOfWeek — each a set of allowed values.
	fields [5]map[int]bool
	expr   string
}

// Parse parses a standard 5-field cron expression.
func Parse(expr string) (Schedule, error) {
	trimmed := strings.TrimSpace(expr)
	parts := strings.Fields(trimmed)
	if len(parts) != 5 {
		return Schedule{}, fmt.Errorf("cronexpr: expected 5 fields (minute hour dom month dow), got %d in %q", len(parts), expr)
	}

	var sched Schedule
	sched.expr = trimmed
	for i, part := range parts {
		set, err := parseField(part, fieldBounds[i][0], fieldBounds[i][1])
		if err != nil {
			return Schedule{}, fmt.Errorf("cronexpr: field %s (%q): %w", fieldNames[i], part, err)
		}
		sched.fields[i] = set
	}
	return sched, nil
}

// parseField parses a single cron field (comma list of literals or */N steps)
// into the set of allowed integer values within [min, max].
func parseField(field string, min, max int) (map[int]bool, error) {
	set := map[int]bool{}
	for _, item := range strings.Split(field, ",") {
		if item == "" {
			return nil, fmt.Errorf("empty item")
		}
		if item == "*" {
			for v := min; v <= max; v++ {
				set[v] = true
			}
			continue
		}
		if strings.HasPrefix(item, "*/") {
			stepStr := strings.TrimPrefix(item, "*/")
			step, err := strconv.Atoi(stepStr)
			if err != nil || step <= 0 {
				return nil, fmt.Errorf("invalid step %q", item)
			}
			for v := min; v <= max; v += step {
				set[v] = true
			}
			continue
		}
		v, err := strconv.Atoi(item)
		if err != nil {
			return nil, fmt.Errorf("invalid value %q", item)
		}
		if v < min || v > max {
			return nil, fmt.Errorf("value %d out of range [%d,%d]", v, min, max)
		}
		set[v] = true
	}
	return set, nil
}

// String returns the original (trimmed) cron expression.
func (s Schedule) String() string {
	return s.expr
}

// Next returns the earliest time strictly after `after` that matches the
// schedule, truncated to minute resolution. It searches up to 4 years ahead
// before giving up (covers e.g. Feb 29 on non-leap-adjacent constraints);
// in practice a match is found within minutes for any sane expression.
func (s Schedule) Next(after time.Time) time.Time {
	t := after.Truncate(time.Minute).Add(time.Minute)
	limit := after.AddDate(4, 0, 0)
	for t.Before(limit) {
		if s.matches(t) {
			return t
		}
		t = t.Add(time.Minute)
	}
	// Should be unreachable for valid expressions; return zero value's far
	// future analog so callers don't mistake this for "due now".
	return limit
}

func (s Schedule) matches(t time.Time) bool {
	minute := t.Minute()
	hour := t.Hour()
	dom := t.Day()
	month := int(t.Month())
	dow := int(t.Weekday())

	if !s.fields[0][minute] || !s.fields[1][hour] || !s.fields[3][month] {
		return false
	}
	// Standard cron OR semantics when both day-of-month and day-of-week are
	// restricted (neither is "*"): match if either matches. If one of them
	// is unrestricted (full range, i.e. effectively "*"), only the other
	// need match.
	domFull := len(s.fields[2]) == fieldBounds[2][1]-fieldBounds[2][0]+1
	dowFull := len(s.fields[4]) == fieldBounds[4][1]-fieldBounds[4][0]+1

	domMatch := s.fields[2][dom]
	dowMatch := s.fields[4][dow]

	switch {
	case domFull && dowFull:
		return true
	case domFull:
		return dowMatch
	case dowFull:
		return domMatch
	default:
		return domMatch || dowMatch
	}
}
