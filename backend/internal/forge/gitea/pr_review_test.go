package gitea

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/myinisjap/agent-task-editor/backend/internal/forge"
)

func TestGetPR_AssociationDraftBot(t *testing.T) {
	g, _ := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/api/v1/repos/own/r/pulls/"):
			writeJSON(t, w, 200, map[string]any{
				"number": 1, "title": "WIP: thing", "state": "open", "html_url": "u",
				"user": map[string]any{"login": "carol"},
				"head": map[string]any{"ref": "f", "sha": "s"}, "base": map[string]any{"ref": "main"},
			})
		case strings.Contains(r.URL.Path, "/collaborators/carol"):
			w.WriteHeader(204)
		default:
			w.WriteHeader(404)
		}
	})
	pr, err := g.GetPR(t.Context(), "own/r", 1)
	if err != nil {
		t.Fatal(err)
	}
	if !pr.Draft || pr.AuthorAssociation != "COLLABORATOR" || pr.AuthorIsBot || pr.State != "open" || pr.HeadSHA != "s" {
		t.Fatalf("pr: %+v", pr)
	}
}

func TestGetPR_OwnerAndBot(t *testing.T) {
	g, _ := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, 200, map[string]any{"number": 1, "state": "closed", "merged": true, "draft": false,
			"user": map[string]any{"login": "Own"}, "head": map[string]any{}, "base": map[string]any{}})
	})
	pr, _ := g.GetPR(t.Context(), "own/r", 1)
	if pr.AuthorAssociation != "OWNER" || pr.State != "merged" || pr.Draft {
		t.Fatalf("pr: %+v", pr)
	}
}

func TestListOpenPRs_Paginates(t *testing.T) {
	g, _ := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "collaborators") {
			w.WriteHeader(404)
			return
		}
		var prs []map[string]any
		n := 50
		if r.URL.Query().Get("page") == "2" {
			n = 3
		}
		for i := 0; i < n; i++ {
			prs = append(prs, map[string]any{"number": i + 1, "state": "open", "user": map[string]any{"login": "x[bot]"}, "head": map[string]any{}, "base": map[string]any{}})
		}
		writeJSON(t, w, 200, prs)
	})
	prs, err := g.ListOpenPRs(t.Context(), "own/r")
	if err != nil || len(prs) != 53 || !prs[0].AuthorIsBot {
		t.Fatalf("n=%d err=%v", len(prs), err)
	}
}

func TestPostCommentReview_EventCommentAndAnchorError(t *testing.T) {
	var got map[string]any
	status := 200
	g, _ := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &got)
		writeJSON(t, w, status, map[string]any{"id": 7})
	})
	ref, err := g.PostCommentReview(t.Context(), "own/r", 2, forge.ReviewSubmission{CommitSHA: "c", Body: "b",
		Comments: []forge.ReviewDraftComment{{Path: "a.go", Line: 4, Body: "x"}}})
	if err != nil || ref != "7" || got["event"] != "COMMENT" {
		t.Fatalf("ref=%q err=%v body=%v", ref, err, got)
	}
	c := got["comments"].([]any)[0].(map[string]any)
	if c["new_position"] != float64(4) || c["path"] != "a.go" {
		t.Fatalf("comment: %v", c)
	}
	status = 422
	_, err = g.PostCommentReview(t.Context(), "own/r", 2, forge.ReviewSubmission{Body: "b", Comments: []forge.ReviewDraftComment{{Path: "a", Line: 1, Body: "x"}}})
	if !errors.Is(err, forge.ErrInlineAnchor) {
		t.Fatalf("err=%v", err)
	}
}
