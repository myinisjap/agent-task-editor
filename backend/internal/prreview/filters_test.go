package prreview

import (
	"testing"

	"github.com/myinisjap/agent-task-editor/backend/internal/forge"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

func baseRepo() gen.Repo {
	return gen.Repo{PrReviewEnabled: 1, PrReviewSkipDrafts: 1, PrReviewAuthorAssocs: `["OWNER","MEMBER","COLLABORATOR"]`}
}

func basePR() forge.PullRequest {
	return forge.PullRequest{Number: 1, State: "open", AuthorAssociation: "MEMBER", HeadRef: "feature/x"}
}

func TestEligible(t *testing.T) {
	cases := []struct {
		name string
		mod  func(*gen.Repo, *forge.PullRequest)
		want bool
	}{
		{"ok", func(*gen.Repo, *forge.PullRequest) {}, true},
		{"disabled", func(r *gen.Repo, _ *forge.PullRequest) { r.PrReviewEnabled = 0 }, false},
		{"closed", func(_ *gen.Repo, p *forge.PullRequest) { p.State = "closed" }, false},
		{"merged", func(_ *gen.Repo, p *forge.PullRequest) { p.State = "merged" }, false},
		{"draft skipped", func(_ *gen.Repo, p *forge.PullRequest) { p.Draft = true }, false},
		{"draft allowed", func(r *gen.Repo, p *forge.PullRequest) { r.PrReviewSkipDrafts = 0; p.Draft = true }, true},
		{"bot", func(_ *gen.Repo, p *forge.PullRequest) { p.AuthorIsBot = true }, false},
		{"ate branch", func(_ *gen.Repo, p *forge.PullRequest) { p.HeadRef = "ate-fix-1234abcd" }, false},
		{"owner", func(_ *gen.Repo, p *forge.PullRequest) { p.AuthorAssociation = "OWNER" }, true},
		{"collaborator lower", func(_ *gen.Repo, p *forge.PullRequest) { p.AuthorAssociation = "collaborator" }, true},
		{"contributor rejected", func(_ *gen.Repo, p *forge.PullRequest) { p.AuthorAssociation = "CONTRIBUTOR" }, false},
		{"none rejected", func(_ *gen.Repo, p *forge.PullRequest) { p.AuthorAssociation = "NONE" }, false},
		{"empty assoc fails closed", func(_ *gen.Repo, p *forge.PullRequest) { p.AuthorAssociation = "" }, false},
		{"contributor allowlisted", func(r *gen.Repo, p *forge.PullRequest) {
			r.PrReviewAuthorAssocs = `["CONTRIBUTOR"]`
			p.AuthorAssociation = "CONTRIBUTOR"
		}, true},
		{"label missing", func(r *gen.Repo, _ *forge.PullRequest) { r.PrReviewLabel = "agent-review" }, false},
		{"label present", func(r *gen.Repo, p *forge.PullRequest) {
			r.PrReviewLabel = "Agent-Review"
			p.Labels = []string{"x", "agent-review"}
		}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r, p := baseRepo(), basePR()
			c.mod(&r, &p)
			got, reason := Eligible(r, p)
			if got != c.want {
				t.Fatalf("Eligible=%v (%s), want %v", got, reason, c.want)
			}
		})
	}
}
