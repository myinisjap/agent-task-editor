// Package lessons holds the validation shared by every way a repo lesson is
// created or edited: the human API handlers and the offline distiller. It is a
// leaf package (stdlib only).
package lessons

import (
	"strings"
	"unicode/utf8"
)

// MaxBodyLen is the maximum lesson body length in characters.
const MaxBodyLen = 300

// Kinds are the allowed lesson kinds.
var Kinds = map[string]bool{"convention": true, "trap": true, "command": true, "decision": true, "preference": true}

// NormList trims, dedupes and rejects entries containing separators.
func NormList(in []string) ([]string, bool) {
	seen := map[string]bool{}
	out := []string{}
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if strings.ContainsAny(s, "\n\r,") {
			return nil, false
		}
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out, true
}

// CheckBody trims body and returns it, or an error message ("" when valid).
func CheckBody(body string) (string, string) {
	body = strings.TrimSpace(body)
	if body == "" {
		return "", "body is required"
	}
	if utf8.RuneCountInString(body) > MaxBodyLen {
		return "", "body must be at most 300 characters"
	}
	return body, ""
}

// Fields is a validated lesson in its storage format.
type Fields struct {
	Kind      string
	Body      string
	PathGlobs string // newline separated
	Stages    string // comma separated
}

// Validate applies the human-API rules to a candidate lesson and returns its
// storage form, or an error message.
func Validate(kind, body string, pathGlobs, stages []string) (Fields, string) {
	if !Kinds[kind] {
		return Fields{}, "invalid kind"
	}
	b, msg := CheckBody(body)
	if msg != "" {
		return Fields{}, msg
	}
	g, ok := NormList(pathGlobs)
	if !ok {
		return Fields{}, "path_globs entries must not contain commas or newlines"
	}
	s, ok := NormList(stages)
	if !ok {
		return Fields{}, "stages entries must not contain commas or newlines"
	}
	return Fields{Kind: kind, Body: b, PathGlobs: strings.Join(g, "\n"), Stages: strings.Join(s, ",")}, ""
}
