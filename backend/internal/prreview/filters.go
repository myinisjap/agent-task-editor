// Package prreview is the review-only PR path: pull requests ATE did not write
// are turned into review tasks (one per PR), an agent reviews them, and the
// findings are posted back on the PR as a comment-type review. It imports only
// storage/gen, forge, workflow and the standard library; it must never import
// agent, ghsync or api.
package prreview

import (
	"encoding/json"
	"strings"

	"github.com/myinisjap/agent-task-editor/backend/internal/forge"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

// DefaultAssociations is the default author-association allowlist.
var DefaultAssociations = []string{"OWNER", "MEMBER", "COLLABORATOR"}

// ATEBranchPrefix is the prefix of branches ATE itself creates for its tasks.
const ATEBranchPrefix = "ate-"

// ParseAssociations decodes repos.pr_review_author_assocs (a JSON array),
// upper-casing entries. Malformed or empty input yields the defaults.
func ParseAssociations(raw string) []string {
	var in []string
	if err := json.Unmarshal([]byte(raw), &in); err != nil || len(in) == 0 {
		return append([]string(nil), DefaultAssociations...)
	}
	out := make([]string, 0, len(in))
	for _, a := range in {
		if a = strings.ToUpper(strings.TrimSpace(a)); a != "" {
			out = append(out, a)
		}
	}
	return out
}

// Eligible reports whether pr should get a review under the repo's policy,
// and, if not, a short reason. It is pure and fails closed: an empty or
// unknown author association is rejected.
func Eligible(repo gen.Repo, pr forge.PullRequest) (bool, string) {
	if repo.PrReviewEnabled == 0 {
		return false, "pr review disabled"
	}
	if pr.State != "open" {
		return false, "pr not open"
	}
	if repo.PrReviewSkipDrafts != 0 && pr.Draft {
		return false, "draft"
	}
	if pr.AuthorIsBot {
		return false, "bot author"
	}
	if strings.HasPrefix(pr.HeadRef, ATEBranchPrefix) {
		return false, "ate task branch"
	}
	assoc := strings.ToUpper(strings.TrimSpace(pr.AuthorAssociation))
	allowed := false
	if assoc != "" {
		for _, a := range ParseAssociations(repo.PrReviewAuthorAssocs) {
			if a == assoc {
				allowed = true
				break
			}
		}
	}
	if !allowed {
		return false, "author association not allowed"
	}
	if want := strings.TrimSpace(repo.PrReviewLabel); want != "" {
		found := false
		for _, l := range pr.Labels {
			if strings.EqualFold(l, want) {
				found = true
				break
			}
		}
		if !found {
			return false, "missing label"
		}
	}
	return true, ""
}
