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
