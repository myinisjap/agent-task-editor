package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/myinisjap/agent-task-editor/backend/internal/prreview"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

// prReviewBody holds the review-only PR policy fields shared by the repo
// create and update bodies. nil means omitted (update leaves it untouched).
type prReviewBody struct {
	PrReviewEnabled      *bool     `json:"pr_review_enabled"`
	PrReviewAuthorAssocs *[]string `json:"pr_review_author_assocs"`
	PrReviewLabel        *string   `json:"pr_review_label"`
	PrReviewSkipDrafts   *bool     `json:"pr_review_skip_drafts"`
	PrReviewWorkflowID   *string   `json:"pr_review_workflow_id"`
	PrReviewTargetLabel  *string   `json:"pr_review_target_label"`
}

func (b prReviewBody) any() bool {
	return b.PrReviewEnabled != nil || b.PrReviewAuthorAssocs != nil || b.PrReviewLabel != nil ||
		b.PrReviewSkipDrafts != nil || b.PrReviewWorkflowID != nil || b.PrReviewTargetLabel != nil
}

var validAssociations = map[string]bool{
	"OWNER": true, "MEMBER": true, "COLLABORATOR": true, "CONTRIBUTOR": true,
	"FIRST_TIME_CONTRIBUTOR": true, "FIRST_TIMER": true, "NONE": true,
}

func boolInt(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

// resolvePRReview merges b over cur and validates the result, writing a 400
// and returning ok=false on the first violation. hasRemote reports whether the
// repo (after this request) has a remote_url.
func (h *ReposHandler) resolvePRReview(w http.ResponseWriter, r *http.Request, b prReviewBody, cur gen.Repo, hasRemote bool) (gen.SetRepoPRReviewParams, bool) {
	p := gen.SetRepoPRReviewParams{
		ID:                   cur.ID,
		PrReviewEnabled:      cur.PrReviewEnabled,
		PrReviewAuthorAssocs: cur.PrReviewAuthorAssocs,
		PrReviewLabel:        cur.PrReviewLabel,
		PrReviewSkipDrafts:   cur.PrReviewSkipDrafts,
		PrReviewWorkflowID:   cur.PrReviewWorkflowID,
		PrReviewTargetLabel:  cur.PrReviewTargetLabel,
	}
	if b.PrReviewEnabled != nil {
		p.PrReviewEnabled = boolInt(*b.PrReviewEnabled)
	}
	if b.PrReviewAuthorAssocs != nil {
		seen := map[string]bool{}
		assocs := []string{}
		for _, a := range *b.PrReviewAuthorAssocs {
			a = strings.ToUpper(strings.TrimSpace(a))
			if !validAssociations[a] {
				Err(w, http.StatusBadRequest, fmt.Sprintf("pr_review_author_assocs: unknown association %q", a))
				return p, false
			}
			if !seen[a] {
				seen[a] = true
				assocs = append(assocs, a)
			}
		}
		if len(assocs) == 0 {
			assocs = prreview.DefaultAssociations
		}
		j, _ := json.Marshal(assocs)
		p.PrReviewAuthorAssocs = string(j)
	}
	if b.PrReviewLabel != nil {
		p.PrReviewLabel = strings.TrimSpace(*b.PrReviewLabel)
	}
	if b.PrReviewSkipDrafts != nil {
		p.PrReviewSkipDrafts = boolInt(*b.PrReviewSkipDrafts)
	}
	if b.PrReviewWorkflowID != nil {
		if id := strings.TrimSpace(*b.PrReviewWorkflowID); id == "" {
			p.PrReviewWorkflowID = nil
		} else {
			p.PrReviewWorkflowID = &id
		}
	}
	if b.PrReviewTargetLabel != nil {
		p.PrReviewTargetLabel = strings.TrimSpace(*b.PrReviewTargetLabel)
	}
	if p.PrReviewEnabled != 0 && !hasRemote {
		Err(w, http.StatusBadRequest, "PR review requires a remote_url")
		return p, false
	}
	if p.PrReviewWorkflowID != nil {
		if _, err := h.q.GetWorkflow(r.Context(), *p.PrReviewWorkflowID); err != nil {
			Err(w, http.StatusBadRequest, "pr_review_workflow_id: workflow not found")
			return p, false
		}
	}
	if p.PrReviewTargetLabel != "" {
		if p.PrReviewWorkflowID == nil {
			Err(w, http.StatusBadRequest, "pr_review_target_label requires pr_review_workflow_id")
			return p, false
		}
		labels, err := h.q.ListWorkflowLabels(r.Context(), *p.PrReviewWorkflowID)
		if err != nil {
			Err(w, http.StatusInternalServerError, err.Error())
			return p, false
		}
		okLabel := false
		for _, l := range labels {
			if l.Name == p.PrReviewTargetLabel && l.IsTerminal == 0 && l.AgentIgnore == 0 {
				okLabel = true
			}
		}
		if !okLabel {
			Err(w, http.StatusBadRequest, "pr_review_target_label must be a non-terminal, non-agent_ignore label of the chosen workflow")
			return p, false
		}
	}
	return p, true
}
