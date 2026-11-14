// Package intakecheck is the task intake assistant: a deterministic
// possible-duplicate check (no model call, computed on read) and an optional,
// advisory model check of a new task. Nothing here moves, edits or archives a
// task.
package intakecheck

import (
	"sort"
	"strings"
	"unicode"
)

// Similarity tuning. The measure is the Jaccard index of normalised token
// sets: |A∩B| / |A∪B|. A candidate is a possible duplicate when the score is
// at least Threshold and at least MinShared tokens are shared (a single shared
// word is never enough; two one-word titles match only when equal).
const (
	Threshold  = 0.5
	MinShared  = 2
	MaxMatches = 5
	// descRunes is how much of a description feeds the token set.
	descRunes = 400
)

// Doc is the text of one task.
type Doc struct {
	ID          string
	Title       string
	Description string
	Label       string
}

// Match is a candidate that looks like the same task.
type Match struct {
	ID    string  `json:"id"`
	Title string  `json:"title"`
	Label string  `json:"label"`
	Score float64 `json:"score"`
}

var stopwords = map[string]bool{
	"the": true, "and": true, "for": true, "with": true, "that": true, "this": true, "from": true,
	"are": true, "was": true, "not": true, "but": true, "you": true, "can": true, "has": true,
	"have": true, "should": true, "when": true, "into": true, "its": true, "our": true,
	"add": true, "fix": true, "make": true, "use": true, "new": true, "need": true, "needs": true,
	"please": true, "task": true, "issue": true, "bug": true, "feature": true,
}

// Tokens normalises text into a set of lower-case, lightly stemmed words.
// Words shorter than 2 characters and common filler words are dropped.
func Tokens(s string) map[string]bool {
	out := map[string]bool{}
	words := strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	for _, w := range words {
		if len([]rune(w)) < 2 || stopwords[w] {
			continue
		}
		out[stem(w)] = true
	}
	return out
}

func stem(w string) string {
	for _, suf := range []string{"ing", "ed", "s"} {
		if strings.HasSuffix(w, suf) && len(w)-len(suf) >= 3 {
			return strings.TrimSuffix(w, suf)
		}
	}
	return w
}

// Jaccard returns the index and the shared-token count of two token sets.
func Jaccard(a, b map[string]bool) (float64, int) {
	if len(a) == 0 || len(b) == 0 {
		return 0, 0
	}
	shared := 0
	for t := range a {
		if b[t] {
			shared++
		}
	}
	union := len(a) + len(b) - shared
	return float64(shared) / float64(union), shared
}

func clipRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

// Score compares two tasks: the better of the title-only and the
// title-plus-description Jaccard index. ok is false below the shared-token
// minimum.
func Score(a, b Doc) (float64, bool) {
	best, bestShared := 0.0, 0
	try := func(x, y map[string]bool) {
		s, n := Jaccard(x, y)
		if s > best {
			best, bestShared = s, n
		}
	}
	try(Tokens(a.Title), Tokens(b.Title))
	if a.Description != "" && b.Description != "" {
		try(Tokens(a.Title+" "+clipRunes(a.Description, descRunes)), Tokens(b.Title+" "+clipRunes(b.Description, descRunes)))
	}
	if best == 0 {
		return 0, false
	}
	if bestShared < MinShared {
		// Allow only an exact match of tiny token sets (e.g. "Dark mode").
		ta, tb := Tokens(a.Title), Tokens(b.Title)
		if !(len(ta) == len(tb) && best == 1) {
			return best, false
		}
	}
	return best, best >= Threshold
}

// Find returns the candidates that look like subject, best first, at most
// MaxMatches. The subject itself (same ID) is skipped.
func Find(subject Doc, candidates []Doc) []Match {
	out := []Match{}
	for _, c := range candidates {
		if c.ID != "" && c.ID == subject.ID {
			continue
		}
		if s, ok := Score(subject, c); ok {
			out = append(out, Match{ID: c.ID, Title: c.Title, Label: c.Label, Score: float64(int(s*100+0.5)) / 100})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	if len(out) > MaxMatches {
		out = out[:MaxMatches]
	}
	return out
}
