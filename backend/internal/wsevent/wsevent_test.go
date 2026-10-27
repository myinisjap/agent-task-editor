package wsevent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

type schemaDoc struct {
	Components struct {
		Schemas map[string]schema `yaml:"schemas"`
	} `yaml:"components"`
}

type schema struct {
	Properties map[string]struct {
		Ref  string   `yaml:"$ref"`
		Enum []string `yaml:"enum"`
	} `yaml:"properties"`
	Required []string `yaml:"required"`
	OneOf    []struct {
		Ref string `yaml:"$ref"`
	} `yaml:"oneOf"`
	Discriminator struct {
		PropertyName string            `yaml:"propertyName"`
		Mapping      map[string]string `yaml:"mapping"`
	} `yaml:"discriminator"`
}

func loadSchemas(t *testing.T) map[string]schema {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "openapi.yaml"))
	if err != nil {
		t.Fatalf("read openapi.yaml: %v", err)
	}
	var doc schemaDoc
	if err := yaml.Unmarshal(b, &doc); err != nil {
		t.Fatalf("parse openapi.yaml: %v", err)
	}
	return doc.Components.Schemas
}

func schemaName(ref string) string { return ref[strings.LastIndex(ref, "/")+1:] }

func keysOf(t *testing.T, v any) []string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("payload %T is not a JSON object: %v", v, err)
	}
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sortedProps(s schema) []string {
	out := make([]string, 0, len(s.Properties))
	for k := range s.Properties {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestPayloadsMatchOpenAPI pins every event payload struct to its WsEvent
// schema in openapi.yaml. full has every optional field set and must marshal
// to exactly the schema's properties; minimal has optionals left empty and
// must still contain every required property.
func TestPayloadsMatchOpenAPI(t *testing.T) {
	schemas := loadSchemas(t)
	src, ref := "github", "o/r#1"
	now := time.Now()
	entry := AgentLogEntry{ID: "i", Type: "stdout", Content: "c", At: now}

	cases := []struct {
		eventType     string
		full, minimal any
	}{
		{TypeTaskLabelChanged, TaskLabelChanged{TaskID: "t", From: "a", To: "b", Note: "n"}, TaskLabelChanged{}},
		{TypeTaskUpdated, TaskUpdated{ID: "t"}, TaskUpdated{}},
		{TypeTaskSideEffect, TaskSideEffect{TaskID: "t", Kind: "k", Status: "done", LastError: "e"}, TaskSideEffect{}},
		{TypeTaskAgentStarted, TaskAgentStarted{TaskID: "t", RunID: "r", AgentName: "a"}, TaskAgentStarted{}},
		{TypeTaskAgentDone, TaskAgentDone{TaskID: "t", RunID: "r", Status: "failed"}, TaskAgentDone{}},
		{TypeTaskNeedsHuman, TaskNeedsHuman{TaskID: "t", RunID: "r", Reason: "agent_question", Message: "m"}, TaskNeedsHuman{}},
		{TypeTaskCostWarning, TaskCostWarning{TaskID: "t", RunID: "r", SpentUSD: 1, BudgetUSD: 2}, TaskCostWarning{}},
		{TypeSystemCostBudgetTripped, SystemCostBudgetTripped{Reason: "daily", Message: "m", DailySpentUSD: 1, DailyLimitUSD: 2, MonthlySpentUSD: 3, MonthlyLimitUSD: 4}, SystemCostBudgetTripped{}},
		{TypeTaskReviewCommentsChange, TaskReviewCommentsChanged{TaskID: "t", RunID: "r", Resolved: 1}, TaskReviewCommentsChanged{}},
		{TypeTaskVerification, TaskVerification{TaskID: "t", RunID: "r", Status: "fail"}, TaskVerification{}},
		{TypeTaskPreview, TaskPreview{TaskID: "t", CaptureID: "c", Status: "ok"}, TaskPreview{}},
		{TypeTaskRateLimited, TaskRateLimited{TaskID: "t", RunID: "r", AgentConfigID: "a", UnblockedAt: "u"}, TaskRateLimited{}},
		{TypeProviderPauseChanged, ProviderPauseChanged{Provider: "p", Paused: true}, ProviderPauseChanged{}},
		{TypeTaskGitStateChanged, TaskGitStateChanged{TaskID: "t", GitState: "pr_open", PRURL: "u"}, TaskGitStateChanged{}},
		{TypeTaskPRMergeableChanged, TaskPRMergeableChanged{TaskID: "t", PRMergeable: "mergeable"}, TaskPRMergeableChanged{}},
		{TypeTaskReviewCommentAdded, TaskReviewCommentAdded{TaskID: "t", CommentID: "c", Source: "s"}, TaskReviewCommentAdded{}},
		{TypeTaskSubtaskConflict, TaskSubtaskConflict{TaskID: "t", ParentID: "p", Files: []string{"f"}}, TaskSubtaskConflict{Files: []string{}}},
		{TypeTaskSourceCommentAdded, TaskSourceCommentAdded{TaskID: "t", CommentID: "c"}, TaskSourceCommentAdded{}},
		{TypeTaskCreated, TaskCreated{ID: "i", Title: "t", Label: "l", RepoID: "r", Source: &src, SourceRef: &ref, ParentID: "p"}, TaskCreated{}},
		{TypeTaskCreatedBulk, TaskCreatedBulk{RepoID: "r", Source: "s", Count: 1, IDs: []string{"i"}}, TaskCreatedBulk{IDs: []string{}}},
		{TypeRepoCloneDone, RepoClone{RepoID: "r", CloneStatus: "ready", CloneError: ""}, RepoClone{}},
		{TypeRepoCloneFailed, RepoClone{RepoID: "r", CloneStatus: "error", CloneError: "e"}, RepoClone{}},
		{TypeAgentLog, AgentLog{RunID: "r", TaskID: "t", Entry: entry}, AgentLog{}},
		{TypeAgentLogReplay, AgentLogReplay{RunID: "r", TaskID: "t", HasMore: true, Entries: []AgentLogEntry{entry}}, AgentLogReplay{Entries: []AgentLogEntry{}}},
		{TypeResync, Resync{}, Resync{}},
	}

	union := schemas["WsEvent"]
	if union.Discriminator.PropertyName != "type" {
		t.Fatalf("WsEvent discriminator = %q, want type", union.Discriminator.PropertyName)
	}

	seen := map[string]bool{}
	for _, c := range cases {
		t.Run(c.eventType, func(t *testing.T) {
			seen[c.eventType] = true

			envRef, ok := union.Discriminator.Mapping[c.eventType]
			if !ok {
				t.Fatalf("event %q missing from WsEvent discriminator mapping", c.eventType)
			}
			env, ok := schemas[schemaName(envRef)]
			if !ok {
				t.Fatalf("envelope schema %q not found", envRef)
			}
			if got := env.Properties["type"].Enum; len(got) != 1 || got[0] != c.eventType {
				t.Errorf("envelope %s type enum = %v, want [%s]", schemaName(envRef), got, c.eventType)
			}
			payloadRef := env.Properties["payload"].Ref
			if payloadRef == "" {
				t.Fatalf("envelope %s has no payload $ref", schemaName(envRef))
			}
			ps, ok := schemas[schemaName(payloadRef)]
			if !ok {
				t.Fatalf("payload schema %q not found", payloadRef)
			}

			if got, want := keysOf(t, c.full), sortedProps(ps); !equal(got, want) {
				t.Errorf("%T keys = %v, openapi %s properties = %v", c.full, got, schemaName(payloadRef), want)
			}
			have := map[string]bool{}
			for _, k := range keysOf(t, c.minimal) {
				have[k] = true
			}
			for _, r := range ps.Required {
				if !have[r] {
					t.Errorf("%T does not always send required property %q (openapi %s)", c.minimal, r, schemaName(payloadRef))
				}
			}
		})
	}

	// Every event in the spec must have a Go struct covered above, and the
	// union's oneOf must list exactly the mapped envelopes.
	for typ := range union.Discriminator.Mapping {
		if !seen[typ] {
			t.Errorf("openapi WsEvent declares %q but no Go payload is tested for it", typ)
		}
	}
	if len(union.OneOf) != len(union.Discriminator.Mapping) {
		t.Errorf("WsEvent oneOf has %d members, discriminator mapping %d", len(union.OneOf), len(union.Discriminator.Mapping))
	}
}
