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
// {"id":..., "version":...} objects; every id must be in allowedLanguages and
// every version must match versionPattern, or ParsePins returns an error and
// callers must not proceed with a partial/best-effort spec.
func ParsePins(jsonStr string) ([]Pin, error) {
	if jsonStr == "" {
		return nil, nil
	}

	var pins []Pin
	if err := json.Unmarshal([]byte(jsonStr), &pins); err != nil {
		return nil, fmt.Errorf("runtime_languages: invalid JSON: %w", err)
	}

	for _, p := range pins {
		if !allowedLanguages[p.ID] {
			return nil, fmt.Errorf("runtime_languages: unsupported language %q", p.ID)
		}
		if !versionPattern.MatchString(p.Version) {
			return nil, fmt.Errorf("runtime_languages: invalid version %q for %q", p.Version, p.ID)
		}
	}

	return pins, nil
}
