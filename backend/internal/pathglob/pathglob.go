// Package pathglob compiles the repo-relative path globs shared by lessons and
// autonomy ask rules. Supports ** (any number of segments, including zero),
// * (anything but /) and ?. A pattern without "/" also matches the basename
// (gitignore-style); a trailing "/" means a directory prefix. Stdlib only.
package pathglob

import (
	"regexp"
	"strings"
)

// Compile turns a glob into a regexp; nil for an empty or invalid pattern.
func Compile(pattern string) *regexp.Regexp {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return nil
	}
	prefix := false
	if strings.HasSuffix(pattern, "/") {
		prefix = true
		pattern = strings.TrimLeft(pattern, "/")
		pattern += "**"
	}
	var b strings.Builder
	b.WriteString("^")
	if !strings.Contains(strings.TrimSuffix(pattern, "**"), "/") && !prefix {
		b.WriteString("(?:.*/)?") // basename match
	}
	for i := 0; i < len(pattern); i++ {
		c := pattern[i]
		switch c {
		case '*':
			if i+1 < len(pattern) && pattern[i+1] == '*' {
				i++
				if i+1 < len(pattern) && pattern[i+1] == '/' {
					i++
					b.WriteString("(?:.*/)?") // "**/" = zero or more dirs
				} else {
					b.WriteString(".*")
				}
			} else {
				b.WriteString("[^/]*")
			}
		case '?':
			b.WriteString("[^/]")
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	b.WriteString("$")
	re, err := regexp.Compile(b.String())
	if err != nil {
		return nil
	}
	return re
}

// Valid reports whether the pattern compiles.
func Valid(pattern string) bool { return Compile(pattern) != nil }

// Match reports whether file matches pattern.
func Match(pattern, file string) bool {
	re := Compile(pattern)
	return re != nil && re.MatchString(file)
}

// Matcher caches compiled patterns. Not safe for concurrent use.
type Matcher struct{ cache map[string]*regexp.Regexp }

// NewMatcher returns an empty Matcher.
func NewMatcher() *Matcher { return &Matcher{cache: map[string]*regexp.Regexp{}} }

// Match is like the package-level Match with caching.
func (m *Matcher) Match(pattern, file string) bool {
	re, ok := m.cache[pattern]
	if !ok {
		re = Compile(pattern)
		m.cache[pattern] = re
	}
	return re != nil && re.MatchString(file)
}

// MatchAny reports whether any glob matches any file.
func (m *Matcher) MatchAny(globs, files []string) bool {
	for _, g := range globs {
		for _, f := range files {
			if m.Match(g, f) {
				return true
			}
		}
	}
	return false
}
