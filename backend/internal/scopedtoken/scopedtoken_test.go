package scopedtoken

import (
	"net/http"
	"testing"
)

func TestRegistry_IssueLookupRevoke(t *testing.T) {
	r := NewRegistry()
	a, _ := r.Issue(Grant{Kind: KindRun, RunID: "r", TaskID: "t"})
	b, _ := r.Issue(Grant{Kind: KindRun, RunID: "r", TaskID: "t"})
	if a == b || len(a) < 64 {
		t.Fatal("tokens must be unique and long")
	}
	if _, ok := r.Lookup(a); !ok {
		t.Fatal("issued token not found")
	}
	r.Revoke(a)
	if _, ok := r.Lookup(a); ok {
		t.Fatal("revoked token still valid")
	}
	if _, ok := r.Lookup(b); !ok {
		t.Fatal("other token should survive")
	}
	if _, ok := r.Lookup(""); ok {
		t.Fatal("empty token accepted")
	}
}

func TestGrant_Allows(t *testing.T) {
	run := Grant{Kind: KindRun, RunID: "r", TaskID: "t1"}
	if !run.Allows(http.MethodPost, "/api/v1/tasks/t1/subtasks") || run.Allows(http.MethodPost, "/api/v1/tasks/t2/subtasks") || run.Allows(http.MethodGet, "/api/v1/tasks/t1") {
		t.Error("run grant scope wrong")
	}
	if (Grant{Kind: KindRun}).Allows(http.MethodPost, "/api/v1/tasks//subtasks") {
		t.Error("empty task id must not match")
	}
	chat := Grant{Kind: KindChat, Chat: "c"}
	if !chat.Allows(http.MethodGet, "/api/v1/repos") || chat.Allows(http.MethodDelete, "/api/v1/repos") || chat.Allows(http.MethodGet, "/api/v1/backup") {
		t.Error("chat grant scope wrong")
	}
	if run.Actor() != "agent:r" || chat.Actor() != "chat:c" {
		t.Error("actor names")
	}
}

func TestChatProfileGrants(t *testing.T) {
	allow := func(profile, method, path string) bool {
		return Grant{Kind: KindChat, Chat: "s", Profile: profile}.Allows(method, path)
	}
	for _, c := range []struct {
		profile, method, path string
		want                  bool
	}{
		{"readonly", "GET", "/api/v1/tasks/1/runs/2/logs", true},
		{"readonly", "POST", "/api/v1/tasks", false},
		{"full", "POST", "/api/v1/tasks", true},
		{"full", "PATCH", "/api/v1/tasks/1/label", true},
		{"full", "POST", "/api/v1/tasks/1/runs/2/reply", true},
		{"full", "DELETE", "/api/v1/tasks/1", false},
		{"full", "DELETE", "/api/v1/repos/1", false},
		{"full", "PUT", "/api/v1/tasks/1", false},
		{"full", "POST", "/api/v1/repos", false},
		{"full", "GET", "/api/v1/settings", false},
		{"full", "GET", "/api/v1/tasks/1/unknown", false},
		{"full", "GET", "/api/v1/tasks//diff", false},
		{"full", "GET", "/api/v1/tasks/../tokens", false},
		{"full", "GET", "/api/v1/tasksx", false},
		{"readonly", "GET", "/api/v1/needs-me", true},
		{"readonly", "GET", "/api/v1/dashboard/cost-by-task", true},
		{"readonly", "GET", "/api/v1/agents/1", true},
		{"readonly", "GET", "/api/v1/schedules", true},
		{"readonly", "POST", "/api/v1/tasks/1/rerun", false},
		{"full", "POST", "/api/v1/tasks/1/runs/2/cancel", true},
		{"full", "POST", "/api/v1/tasks/1/pr", true},
		{"full", "PUT", "/api/v1/agents/1", false},
		{"full", "POST", "/api/v1/schedules", false},
		{"admin", "PUT", "/api/v1/agents/1", true},
		{"admin", "PUT", "/api/v1/schedules/1", true},
		{"admin", "DELETE", "/api/v1/schedules/1", false},
		{"admin", "POST", "/api/v1/repos", true},
		{"admin", "PUT", "/api/v1/workflows/1/yaml", true},
		{"admin", "POST", "/api/v1/workflows/import", true},
		{"admin", "DELETE", "/api/v1/repos/1", false},
		{"admin", "DELETE", "/api/v1/workflows/1", false},
		{"", "POST", "/api/v1/agents", false},
		{"", "GET", "/api/v1/tasks", false},
		{"bogus", "GET", "/api/v1/repos", false},
	} {
		if got := allow(c.profile, c.method, c.path); got != c.want {
			t.Errorf("%q %s %s = %v, want %v", c.profile, c.method, c.path, got, c.want)
		}
	}
}

func TestGrant_NeverAllowsSkills(t *testing.T) {
	paths := []string{"/api/v1/skills", "/api/v1/skills/abc", "/api/v1/agents/abc/skills", "/api/v1/repos/abc/skills"}
	grants := []Grant{{Kind: KindRun, RunID: "r", TaskID: "t1"}, {Kind: KindChat, Chat: "c"}}
	for _, p := range []string{"readonly", "full", "admin"} {
		grants = append(grants, Grant{Kind: KindChat, Chat: "c", Profile: p})
	}
	for _, g := range grants {
		for _, p := range paths {
			for _, m := range []string{"GET", "POST", "PUT", "PATCH", "DELETE"} {
				if g.Allows(m, p) {
					t.Errorf("grant %+v must not allow %s %s", g, m, p)
				}
			}
		}
	}
}
