package prreview

import (
	"encoding/json"
	"fmt"
)

// StoredComment is one inline finding stored in task_pr_review_posts.comments_json.
type StoredComment struct {
	Path      string `json:"path"`
	Line      int    `json:"line"`
	StartLine int    `json:"start_line,omitempty"`
	Body      string `json:"body"`
}

// FindingID is the stable id of the i-th (0-based) inline comment of review seq.
func FindingID(seq int64, i int) string { return fmt.Sprintf("F%d-%d", seq, i+1) }

// ParseComments decodes comments_json; malformed input yields nil.
func ParseComments(raw string) []StoredComment {
	var c []StoredComment
	_ = json.Unmarshal([]byte(raw), &c)
	return c
}

// ParseStrings decodes a JSON string array; malformed input yields nil.
func ParseStrings(raw string) []string {
	var s []string
	_ = json.Unmarshal([]byte(raw), &s)
	return s
}

// Finding is a prior finding with its id, for prompts and "resolved" lists.
type Finding struct {
	ID   string
	Path string
	Line int
	Body string
}

// Marker is the hidden marker embedded in every posted review body so a retry
// can detect that the post already reached the forge.
func Marker(postID string) string { return "<!-- ate-pr-review:" + postID + " -->" }
