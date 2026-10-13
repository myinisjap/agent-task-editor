package handlers

import (
	"container/list"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"

	"github.com/myinisjap/agent-task-editor/backend/internal/ghsync"
	"github.com/myinisjap/agent-task-editor/backend/internal/metrics"
)

const (
	// webhookMaxBody caps the accepted payload. GitHub allows up to 25 MB but
	// the events handled here are a few KB to a few hundred KB.
	webhookMaxBody = 2 << 20
	// webhookDedupeSize is how many recent X-GitHub-Delivery IDs are remembered.
	webhookDedupeSize = 512
)

// PRRefresher is satisfied by *ghsync.Syncer.
type PRRefresher interface {
	// NoteWebhook records receipt of a delivery (lets polling back off).
	NoteWebhook()
	// RefreshMatching schedules a refresh of tasks matching ref and returns
	// how many matched.
	RefreshMatching(ctx context.Context, ref ghsync.PRRef) int
}

// GitHubWebhookHandler receives GitHub webhook deliveries and triggers the
// same per-task refresh the ghsync poller uses. Payload fields are only used
// to find the affected tasks; state is always re-fetched from the forge.
type GitHubWebhookHandler struct {
	secret    []byte
	refresher PRRefresher
	seen      *deliveryLRU
}

// NewGitHubWebhookHandler returns a handler. An empty secret disables it
// (every request gets 404).
func NewGitHubWebhookHandler(secret string, refresher PRRefresher) *GitHubWebhookHandler {
	return &GitHubWebhookHandler{secret: []byte(secret), refresher: refresher, seen: newDeliveryLRU(webhookDedupeSize)}
}

// deliveryLRU is a small mutex-guarded LRU set of delivery IDs.
type deliveryLRU struct {
	mu    sync.Mutex
	max   int
	order *list.List
	items map[string]*list.Element
}

func newDeliveryLRU(max int) *deliveryLRU {
	return &deliveryLRU{max: max, order: list.New(), items: map[string]*list.Element{}}
}

// add records id and reports whether it was already present.
func (l *deliveryLRU) add(id string) (seen bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if el, ok := l.items[id]; ok {
		l.order.MoveToFront(el)
		return true
	}
	l.items[id] = l.order.PushFront(id)
	if l.order.Len() > l.max {
		oldest := l.order.Back()
		l.order.Remove(oldest)
		delete(l.items, oldest.Value.(string))
	}
	return false
}

// validWebhookSignature checks header ("sha256=<hex>") against the HMAC of
// body in constant time.
func validWebhookSignature(secret, body []byte, header string) bool {
	hexSig, ok := strings.CutPrefix(header, "sha256=")
	if !ok {
		return false
	}
	got, err := hex.DecodeString(hexSig)
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write(body) // hash.Hash.Write never returns an error
	return hmac.Equal(got, mac.Sum(nil))
}

func webhookMetric(event, result string) {
	metrics.GithubWebhookEventsTotal.WithLabelValues(event, result).Inc()
}

// webhookEvents is the set of event names used as metric labels.
var webhookEvents = map[string]bool{
	"ping": true, "pull_request": true, "pull_request_review": true,
	"pull_request_review_comment": true, "issue_comment": true,
	"check_suite": true, "check_run": true, "status": true,
}

type webhookPayload struct {
	Action     string `json:"action"`
	Number     int    `json:"number"`
	Sha        string `json:"sha"` // status
	Repository struct {
		FullName string `json:"full_name"`
	} `json:"repository"`
	PullRequest struct {
		Number int `json:"number"`
		Head   struct {
			Ref string `json:"ref"`
			Sha string `json:"sha"`
		} `json:"head"`
	} `json:"pull_request"`
	Issue struct {
		Number      int             `json:"number"`
		PullRequest json.RawMessage `json:"pull_request"`
	} `json:"issue"`
	CheckSuite checkPayload `json:"check_suite"`
	CheckRun   struct {
		HeadSha      string       `json:"head_sha"`
		CheckSuite   checkPayload `json:"check_suite"`
		PullRequests []struct {
			Number int `json:"number"`
		} `json:"pull_requests"`
	} `json:"check_run"`
	Branches []struct {
		Name string `json:"name"`
	} `json:"branches"` // status
}

type checkPayload struct {
	HeadSha      string `json:"head_sha"`
	HeadBranch   string `json:"head_branch"`
	PullRequests []struct {
		Number int `json:"number"`
	} `json:"pull_requests"`
}

// refFor maps an event to the PR reference used to find tasks. ok=false means
// the event/action is not one we act on.
func refFor(event string, p webhookPayload) (ref ghsync.PRRef, ok bool) {
	ref.Repo = p.Repository.FullName
	switch event {
	case "pull_request":
		switch p.Action {
		case "opened", "closed", "reopened", "synchronize", "edited", "ready_for_review", "converted_to_draft":
		default:
			return ref, false
		}
		n := p.PullRequest.Number
		if n == 0 {
			n = p.Number
		}
		ref.Numbers = []int{n}
		ref.Branches = []string{p.PullRequest.Head.Ref}
		ref.HeadSHA = p.PullRequest.Head.Sha
	case "pull_request_review", "pull_request_review_comment":
		ref.Numbers = []int{p.PullRequest.Number}
		ref.Branches = []string{p.PullRequest.Head.Ref}
		ref.HeadSHA = p.PullRequest.Head.Sha
	case "issue_comment":
		if len(p.Issue.PullRequest) == 0 || string(p.Issue.PullRequest) == "null" {
			return ref, false // comment on a plain issue
		}
		ref.Numbers = []int{p.Issue.Number}
	case "check_suite":
		if p.Action != "completed" {
			return ref, false
		}
		ref.HeadSHA = p.CheckSuite.HeadSha
		ref.Branches = []string{p.CheckSuite.HeadBranch}
		for _, pr := range p.CheckSuite.PullRequests {
			ref.Numbers = append(ref.Numbers, pr.Number)
		}
	case "check_run":
		if p.Action != "completed" {
			return ref, false
		}
		ref.HeadSHA = p.CheckRun.HeadSha
		ref.Branches = []string{p.CheckRun.CheckSuite.HeadBranch}
		for _, pr := range p.CheckRun.PullRequests {
			ref.Numbers = append(ref.Numbers, pr.Number)
		}
	case "status":
		ref.HeadSHA = p.Sha
		for _, b := range p.Branches {
			ref.Branches = append(ref.Branches, b.Name)
		}
	default:
		return ref, false
	}
	return ref, ref.Repo != ""
}

// Handle serves POST /api/v1/github/webhook. It is mounted outside bearer
// auth; the HMAC signature is the authentication.
func (h *GitHubWebhookHandler) Handle(w http.ResponseWriter, r *http.Request) {
	if len(h.secret) == 0 || h.refresher == nil {
		http.NotFound(w, r)
		return
	}
	event := r.Header.Get("X-GitHub-Event")
	label := event
	if !webhookEvents[label] {
		label = "other"
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, webhookMaxBody))
	if err != nil {
		var tooBig *http.MaxBytesError
		status := http.StatusBadRequest
		if errors.As(err, &tooBig) {
			status = http.StatusRequestEntityTooLarge
		}
		webhookMetric(label, "bad_request")
		Err(w, status, "cannot read webhook body")
		return
	}
	if !validWebhookSignature(h.secret, body, r.Header.Get("X-Hub-Signature-256")) {
		webhookMetric(label, "invalid_signature")
		Err(w, http.StatusUnauthorized, "invalid or missing signature")
		return
	}

	if event == "ping" {
		webhookMetric(label, "ping")
		JSON(w, http.StatusOK, map[string]string{"status": "pong"})
		return
	}
	h.refresher.NoteWebhook()

	if id := r.Header.Get("X-GitHub-Delivery"); id != "" && h.seen.add(id) {
		webhookMetric(label, "duplicate")
		JSON(w, http.StatusOK, map[string]string{"status": "duplicate"})
		return
	}

	var p webhookPayload
	if err := json.Unmarshal(body, &p); err != nil {
		webhookMetric(label, "bad_request")
		Err(w, http.StatusBadRequest, "invalid JSON payload")
		return
	}
	ref, ok := refFor(event, p)
	if !ok {
		webhookMetric(label, "ignored")
		w.WriteHeader(http.StatusNoContent)
		return
	}

	// Respond now; matching and the forge refresh happen in the background.
	// context.WithoutCancel: the request context dies when we return.
	ctx := context.WithoutCancel(r.Context())
	go func() {
		n := h.refresher.RefreshMatching(ctx, ref)
		result := "refreshed"
		if n == 0 {
			result = "unmatched"
		}
		webhookMetric(label, result)
		slog.Info("github webhook processed", "event", event, "repo", ref.Repo, "matched_tasks", n)
	}()
	JSON(w, http.StatusAccepted, map[string]string{"status": "accepted"})
}
