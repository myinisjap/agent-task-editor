// Package intakehook turns a verified inbound webhook payload into a task (or
// a follow-up comment on an existing open task) for an intake endpoint. It
// does no HTTP and no signature checking; the API handler does both before
// calling in.
//
// # Trust model
//
// Payload content is untrusted third-party data. Tasks therefore get
// source = "webhook", land on the workflow's human-review gate label unless
// the admin-configured endpoint target_label says otherwise, and follow-up
// events for a deduped task are stored as task_source_comments, which the
// agent prompt renders inside the untrusted-comments fence.
package intakehook

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/myinisjap/agent-task-editor/backend/internal/intake"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
	"github.com/myinisjap/agent-task-editor/backend/internal/workflow"
	"github.com/myinisjap/agent-task-editor/backend/internal/wsevent"
)

// ErrNoWorkflow means neither the endpoint, a matched rule, nor the repo
// names a usable workflow.
var ErrNoWorkflow = errors.New("intake endpoint has no workflow: set one on the endpoint or the repo")

// Publisher is satisfied by *ws.Hub.
type Publisher interface {
	Publish(eventType string, payload any)
}

// Service shapes and ingests webhook events.
type Service struct {
	q   *gen.Queries
	hub Publisher

	mu    sync.Mutex
	locks map[string]*sync.Mutex
}

// NewService builds a Service. db is accepted for symmetry with the other
// services; all access goes through q.
func NewService(_ *sql.DB, q *gen.Queries, hub Publisher) *Service {
	return &Service{q: q, hub: hub, locks: map[string]*sync.Mutex{}}
}

// Shaped is the fully resolved task (or dedupe hit) for a payload.
type Shaped struct {
	Title          string
	Description    string
	DedupeKey      string
	Type           string
	Label          string
	WorkflowID     string
	Priority       int64
	MaxCostUsd     float64
	MatchedRuleID  *string
	ExistingTaskID string

	refPrefix string
}

// Result reports what Receive did.
type Result struct {
	TaskID  string
	Created bool
	Deduped bool
}

// Shape resolves everything about the task a payload would produce without
// creating anything.
//
// Precedence:
//   - workflow: endpoint.workflow_id, else matched rule apply_workflow_id,
//     else repo workflow.
//   - label: the endpoint's own target_label (admin-set, trusted, like a
//     schedule's) if it exists in the workflow; else the rule's
//     apply_target_label if it exists AND intake.AutoStartAllowed. Webhook
//     candidates never carry an author association, so a rule target other
//     than the gate always fails closed, intentionally: payload text must
//     not be able to start an agent without human review. Else the gate.
//   - priority / max cost / type come from the matched rule (and its
//     template), type defaulting to "feature".
func (s *Service) Shape(ctx context.Context, ep gen.IntakeEndpoint, payload any) (Shaped, error) {
	repo, err := s.q.GetRepo(ctx, ep.RepoID)
	if err != nil {
		return Shaped{}, fmt.Errorf("load repo: %w", err)
	}

	sh := Shaped{
		Title:       intake.RenderTitle(ep.TitleTemplate, payload, ep.Name),
		Description: intake.RenderDescription(ep.DescriptionTemplate, payload, ep.Name),
		DedupeKey:   intake.RenderDedupeKey(ep.DedupeKeyTemplate, payload),
		Type:        "feature",
	}

	rules, err := s.q.ListEnabledIntakeRules(ctx)
	if err != nil {
		rules = nil // fail open to defaults, like the other callers
	}
	decision, rule, matched := matchRule(rules, intake.Candidate{
		Source: "webhook",
		RepoID: repo.ID,
		Title:  sh.Title,
		Body:   sh.Description,
	})

	wfID := ""
	switch {
	case ep.WorkflowID != nil && *ep.WorkflowID != "":
		wfID = *ep.WorkflowID
	case matched && decision.WorkflowID != nil && *decision.WorkflowID != "":
		wfID = *decision.WorkflowID
	case repo.WorkflowID != nil:
		wfID = *repo.WorkflowID
	}
	if wfID == "" {
		return Shaped{}, ErrNoWorkflow
	}
	labels, err := s.q.ListWorkflowLabels(ctx, wfID)
	if err != nil {
		return Shaped{}, fmt.Errorf("load workflow labels: %w", err)
	}
	gate, first := workflow.GateLabel(labels)
	if gate == "" {
		gate = first
	}
	if gate == "" {
		return Shaped{}, errors.New("workflow has no labels")
	}
	has := func(name string) bool {
		for _, l := range labels {
			if l.Name == name {
				return true
			}
		}
		return false
	}
	sh.WorkflowID = wfID
	sh.Label = gate
	switch {
	case ep.TargetLabel != "" && has(ep.TargetLabel):
		sh.Label = ep.TargetLabel
	case matched && decision.TargetLabel != "" && has(decision.TargetLabel) &&
		intake.AutoStartAllowed(rule, decision.TargetLabel == gate):
		sh.Label = decision.TargetLabel
	}

	if matched {
		if decision.Priority != nil {
			sh.Priority = *decision.Priority
		}
		if decision.MaxCostUsd != nil {
			sh.MaxCostUsd = *decision.MaxCostUsd
		}
		id := decision.RuleID
		sh.MatchedRuleID = &id
		if decision.TemplateID != nil && *decision.TemplateID != "" {
			if tmpl, err := s.q.GetTaskTemplate(ctx, *decision.TemplateID); err == nil {
				sh.Type = tmpl.Type
				if tmpl.Description != "" {
					sh.Description = tmpl.Description + "\n\n" + sh.Description
				}
			}
		}
	}

	if sh.DedupeKey != "" {
		sum := sha256.Sum256([]byte(sh.DedupeKey))
		sh.refPrefix = ep.ID + "#" + hex.EncodeToString(sum[:])[:32]
		id, err := s.q.GetOpenIntakeTaskIDByRefPrefix(ctx, &sh.refPrefix)
		if err == nil {
			sh.ExistingTaskID = id
		} else if !errors.Is(err, sql.ErrNoRows) {
			return Shaped{}, fmt.Errorf("dedupe lookup: %w", err)
		}
	}
	return sh, nil
}

func (s *Service) lockFor(id string) *sync.Mutex {
	s.mu.Lock()
	defer s.mu.Unlock()
	l, ok := s.locks[id]
	if !ok {
		l = &sync.Mutex{}
		s.locks[id] = l
	}
	return l
}

// Receive creates a task for payload, or appends a source comment to the
// open task a matching dedupe key points at.
func (s *Service) Receive(ctx context.Context, ep gen.IntakeEndpoint, payload any) (Result, error) {
	l := s.lockFor(ep.ID)
	l.Lock()
	defer l.Unlock()

	sh, err := s.Shape(ctx, ep, payload)
	if err != nil {
		return Result{}, err
	}
	now := time.Now()
	touch := func() {
		_ = s.q.TouchIntakeEndpointReceived(ctx, gen.TouchIntakeEndpointReceivedParams{LastReceivedAt: &now, ID: ep.ID})
	}

	if sh.ExistingTaskID != "" {
		body := sh.Title + "\n\n" + sh.Description
		if len(body) > intake.MaxDescriptionLen {
			body = body[:intake.MaxDescriptionLen]
		}
		c, err := s.q.CreateTaskSourceComment(ctx, gen.CreateTaskSourceCommentParams{
			ID:                uuid.NewString(),
			TaskID:            sh.ExistingTaskID,
			ExternalID:        "intake:" + uuid.NewString(),
			Author:            "intake:" + ep.Name,
			Body:              body,
			ExternalCreatedAt: storage.FormatTime(now),
		})
		if err != nil {
			return Result{}, fmt.Errorf("append comment: %w", err)
		}
		touch()
		s.hub.Publish(wsevent.TypeTaskSourceCommentAdded, wsevent.TaskSourceCommentAdded{TaskID: sh.ExistingTaskID, CommentID: c.ID})
		return Result{TaskID: sh.ExistingTaskID, Deduped: true}, nil
	}

	ref := ep.ID + "#" + uuid.NewString()
	if sh.refPrefix != "" {
		ref = sh.refPrefix + "#" + strconv.FormatInt(now.UnixNano(), 10)
	}
	task, err := s.q.CreateSourcedTask(ctx, gen.CreateSourcedTaskParams{
		ID:            uuid.NewString(),
		Title:         sh.Title,
		Description:   sh.Description,
		Type:          sh.Type,
		Label:         sh.Label,
		RepoID:        ep.RepoID,
		WorkflowID:    sh.WorkflowID,
		Attachments:   "[]",
		Source:        "webhook",
		SourceRef:     ref,
		Priority:      sh.Priority,
		MaxCostUsd:    sh.MaxCostUsd,
		MatchedRuleID: sh.MatchedRuleID,
	})
	if err != nil {
		return Result{}, fmt.Errorf("create task: %w", err)
	}
	touch()
	s.hub.Publish(wsevent.TypeTaskCreated, wsevent.TaskCreated{
		ID:        task.ID,
		Title:     task.Title,
		Label:     task.Label,
		RepoID:    task.RepoID,
		Source:    &task.Source,
		SourceRef: &task.SourceRef,
	})
	return Result{TaskID: task.ID, Created: true}, nil
}

// matchRule runs intake.Match and also returns the raw matched rule.
func matchRule(rules []gen.IntakeRule, c intake.Candidate) (intake.Decision, gen.IntakeRule, bool) {
	d, ok := intake.Match(rules, c)
	if !ok {
		return intake.Decision{}, gen.IntakeRule{}, false
	}
	for _, r := range rules {
		if r.ID == d.RuleID {
			return d, r, true
		}
	}
	return d, gen.IntakeRule{}, false
}
