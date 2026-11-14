// Package repomap generates, stores and serves the per-repo "repo map": a
// short wiki of the codebase (fixed outline, hard size caps, every page tied
// to the commit it was generated from) that planning agents receive as
// orientation. Generated content is untrusted-ish: it is always fenced in the
// prompt and never overrides task instructions. See docs/repo-map.md.
package repomap

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

// Hard size limits.
const (
	// MaxPageChars caps one page.
	MaxPageChars = 5000
	// MaxTotalChars caps all pages of one map together.
	MaxTotalChars = 20000
	// MaxInjectedChars caps the REPO MAP prompt section (Overview + Layout).
	MaxInjectedChars = 2000
	// KeepVersions is how many successful runs keep their pages.
	KeepVersions = 3
)

// PageSpec is one entry of the fixed outline.
type PageSpec struct {
	Slug  string
	Title string
	Brief string
}

// Outline is the fixed page outline, in display order.
var Outline = []PageSpec{
	{"overview", "Overview", "what the project is, who it is for, the main components and how they fit together"},
	{"layout", "Layout", "the top-level directories and what lives where (one line each; name the important files)"},
	{"build-test-run", "How to build, test and run", "the real commands to build, test, lint and run locally, and any prerequisites"},
	{"key-flows", "Key flows", "the 3-6 most important end-to-end flows and the files they pass through"},
	{"conventions", "Conventions and traps", "coding conventions, generated files, and non-obvious pitfalls a newcomer would trip on"},
}

// Page is one generated page.
type Page struct {
	Slug  string
	Title string
	Body  string
}

const (
	pageStart = "<<<PAGE:"
	pageEnd   = "<<<END>>>"
)

var pageRe = regexp.MustCompile(`(?s)<<<PAGE:([a-z-]+)>>>\s*\n(.*?)<<<END>>>`)

// Parse extracts the outline pages from model output. Unknown slugs are
// dropped; when a slug appears more than once the last occurrence wins (the
// same text can be logged twice by streaming providers). Pages come back in
// outline order, each trimmed and capped by MaxPageChars; missing pages are
// simply absent.
func Parse(text string) []Page {
	found := map[string]string{}
	for _, m := range pageRe.FindAllStringSubmatch(text, -1) {
		body := strings.TrimSpace(m[2])
		if body != "" {
			found[m[1]] = body
		}
	}
	var out []Page
	for _, spec := range Outline {
		if body, ok := found[spec.Slug]; ok {
			out = append(out, Page{Slug: spec.Slug, Title: spec.Title, Body: truncate(body, MaxPageChars)})
		}
	}
	return out
}

// EnforceCaps trims pages so their combined size is at most MaxTotalChars.
// Earlier pages keep their size; later ones are shortened or dropped.
func EnforceCaps(pages []Page) []Page {
	var out []Page
	left := MaxTotalChars
	for _, p := range pages {
		p.Body = truncate(p.Body, MaxPageChars)
		if left <= 0 {
			break
		}
		if utf8.RuneCountInString(p.Body) > left {
			p.Body = truncate(p.Body, left)
		}
		left -= utf8.RuneCountInString(p.Body)
		out = append(out, p)
	}
	return out
}

// TotalChars is the combined body size of pages in characters.
func TotalChars(pages []Page) int {
	n := 0
	for _, p := range pages {
		n += utf8.RuneCountInString(p.Body)
	}
	return n
}

// Required reports whether pages contain the pages injection depends on.
func Required(pages []Page) bool {
	have := map[string]bool{}
	for _, p := range pages {
		have[p.Slug] = true
	}
	return have["overview"] && have["layout"]
}

const truncMarker = "\n\n_(truncated)_"

// truncate shortens s to at most max characters (marker included), cutting at
// a line boundary when one is near the end.
func truncate(s string, max int) string {
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	keep := max - utf8.RuneCountInString(truncMarker)
	if keep < 0 {
		keep = 0
	}
	r := []rune(s)[:keep]
	cut := string(r)
	if i := strings.LastIndex(cut, "\n"); i > len(cut)*3/4 {
		cut = cut[:i]
	}
	if max < utf8.RuneCountInString(truncMarker)*2 {
		return strings.TrimRight(cut, " \n")
	}
	return strings.TrimRight(cut, " \n") + truncMarker
}
