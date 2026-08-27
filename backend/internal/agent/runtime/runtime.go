// Package runtime implements per-repo agent toolchain pins (mise-managed
// language versions). A repo with no pins configured (repos.runtime_languages
// == "") must behave byte-identically to before this package existed — see
// ParsePins and the empty-string case it returns for.
package runtime

import (
	"encoding/json"
	"fmt"
	"regexp"
)

// Pin is one language/version pair from a repo's runtime_languages column.
type Pin struct {
	ID      string `json:"id"`
	Version string `json:"version"`
}

// allowedLanguages is the fixed set of mise-installable languages this
// feature supports. Not user-extensible: these strings become argv elements
// passed to `mise`/`mise x`, so the set is deliberately closed rather than
// accepting arbitrary mise plugin names.
var allowedLanguages = map[string]bool{
	"go":     true,
	"node":   true,
	"python": true,
	"rust":   true,
	"ruby":   true,
	"java":   true,
}

// versionPattern matches a safe mise version string: starts with an
// alphanumeric (never '-', which could be misread as a flag), then up to 31
// more alphanumerics/./_/- characters. No '@' (that's the id/version
// separator in `mise x id@version`) and no spaces. Pin.Version becomes an
// argv element passed to mise/uv subprocesses, so this is a trust-boundary
// validation, not just a UX nicety.
var versionPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,31}$`)

// ParsePins decodes and validates a repo's runtime_languages column value.
// An empty string returns (nil, nil) — the zero value callers must treat as
// "no runtime configured, take the exact pre-feature code path" (see the
// package doc comment). A non-empty value must be a JSON array of
// {"id":..., "version":...} objects; every id must be in allowedLanguages,
// every version must match versionPattern, and no language id may repeat
// (two pins for the same language is ambiguous config — which one wins?),
// or ParsePins returns an error and callers must not proceed with a
// partial/best-effort spec. Every writer of runtime_languages routes through
// this function, so this is the single place that enforces "at most one pin
// per language" — the frontend form separately blocks the same case
// client-side (ReposPage.tsx's validateRuntimeRows) with matching error
// copy, but a direct API write bypasses that and must still be rejected here.
func ParsePins(jsonStr string) ([]Pin, error) {
	if jsonStr == "" {
		return nil, nil
	}

	var pins []Pin
	if err := json.Unmarshal([]byte(jsonStr), &pins); err != nil {
		return nil, fmt.Errorf("runtime_languages: invalid JSON: %w", err)
	}

	seen := make(map[string]bool, len(pins))
	for _, p := range pins {
		if !allowedLanguages[p.ID] {
			return nil, fmt.Errorf("runtime_languages: unsupported language %q", p.ID)
		}
		if !versionPattern.MatchString(p.Version) {
			return nil, fmt.Errorf("runtime_languages: invalid version %q for %q", p.Version, p.ID)
		}
		if seen[p.ID] {
			return nil, fmt.Errorf("runtime_languages: duplicate runtime language: %s", p.ID)
		}
		seen[p.ID] = true
	}

	return pins, nil
}
