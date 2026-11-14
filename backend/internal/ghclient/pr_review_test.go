package ghclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/myinisjap/agent-task-editor/backend/internal/forge"
)

func TestGetPRMapping(t *testing.T) {
	fakeAPI(t, func(r recorded, w http.ResponseWriter, _ *http.Request) {
		jsonOK(w, `{"number":5,"title":"T","body":"B","html_url":"https://x/pull/5","state":"closed","merged_at":"2025-01-01T00:00:00Z",
		"draft":true,"author_association":"member","user":{"login":"dependabot[bot]","type":"Bot"},
		"head":{"ref":"feat","sha":"abc","repo":{"full_name":"fork/r"}},"base":{"ref":"main"},"labels":[{"name":"agent-review"}]}`)
	})
	pr, err := GetPR(context.Background(), "o/r", 5)
	if err != nil {
		t.Fatal(err)
	}
	if !pr.AuthorIsBot || !pr.Draft || pr.State != "merged" || pr.AuthorAssociation != "MEMBER" ||
		pr.HeadRef != "feat" || pr.HeadSHA != "abc" || pr.HeadRepo != "fork/r" || pr.BaseRef != "main" || len(pr.Labels) != 1 {
		t.Fatalf("mapping: %+v", pr)
	}
}

func TestListOpenPRsPaginates(t *testing.T) {
	var base string
	srvReqs := fakeAPI(t, func(r recorded, w http.ResponseWriter, req *http.Request) {
		if strings.Contains(r.Query, "page=2") {
			jsonOK(w, `[{"number":2,"state":"open","user":{"login":"b"},"head":{},"base":{}}]`)
			return
		}
		w.Header().Set("Link", fmt.Sprintf(`<%s%s?state=open&per_page=100&page=2>; rel="next"`, "http://"+req.Host, r.Path))
		jsonOK(w, `[{"number":1,"state":"open","user":{"login":"a"},"head":{},"base":{}}]`)
	})
	_ = base
	prs, err := ListOpenPRs(context.Background(), "o/r")
	if err != nil || len(prs) != 2 || len(*srvReqs) != 2 {
		t.Fatalf("prs=%d reqs=%d err=%v", len(prs), len(*srvReqs), err)
	}
}

func TestPostCommentReviewAlwaysComment(t *testing.T) {
	reqs := fakeAPI(t, func(r recorded, w http.ResponseWriter, _ *http.Request) { jsonOK(w, `{"id":99}`) })
	ref, err := PostCommentReview(context.Background(), "o/r", 3, forge.ReviewSubmission{
		CommitSHA: "abc", Body: "hello",
		Comments: []forge.ReviewDraftComment{{Path: "a.go", Line: 5, StartLine: 2, Body: "x"}, {Path: "b.go", Line: 4, StartLine: 4, Body: "y"}},
	})
	if err != nil || ref != "99" {
		t.Fatalf("ref=%q err=%v", ref, err)
	}
	r := (*reqs)[0]
	if r.Method != "POST" || r.Path != "/repos/o/r/pulls/3/reviews" {
		t.Fatalf("request: %+v", r)
	}
	var body map[string]any
	_ = json.Unmarshal([]byte(r.Body), &body)
	if body["event"] != "COMMENT" || body["commit_id"] != "abc" {
		t.Fatalf("body: %s", r.Body)
	}
	cs := body["comments"].([]any)
	c0, c1 := cs[0].(map[string]any), cs[1].(map[string]any)
	if c0["side"] != "RIGHT" || c0["start_line"] != float64(2) || c0["start_side"] != "RIGHT" {
		t.Fatalf("c0: %v", c0)
	}
	if _, has := c1["start_line"]; has {
		t.Fatalf("start_line must be omitted when equal to line: %v", c1)
	}
}

func TestPostCommentReviewInlineRejectedMapsToErrInlineAnchor(t *testing.T) {
	fakeAPI(t, func(r recorded, w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"message":"Unprocessable Entity","errors":["Line could not be resolved"]}`))
	})
	_, err := PostCommentReview(context.Background(), "o/r", 3, forge.ReviewSubmission{Body: "b", Comments: []forge.ReviewDraftComment{{Path: "a", Line: 1, Body: "x"}}})
	if !errors.Is(err, forge.ErrInlineAnchor) {
		t.Fatalf("err=%v", err)
	}
	// Without inline comments a 422 is not an anchor problem.
	_, err = PostCommentReview(context.Background(), "o/r", 3, forge.ReviewSubmission{Body: "b"})
	if err == nil || errors.Is(err, forge.ErrInlineAnchor) {
		t.Fatalf("err=%v", err)
	}
}
