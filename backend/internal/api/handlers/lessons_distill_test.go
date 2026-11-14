package handlers_test

import (
	"context"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/myinisjap/agent-task-editor/backend/internal/api/handlers"
	"github.com/myinisjap/agent-task-editor/backend/internal/autonomy/propose"
	"github.com/myinisjap/agent-task-editor/backend/internal/lessons/distill"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

func TestLessonDistill_RunsAndDismiss(t *testing.T) {
	e := newRulesEnv(t)
	ctx := context.Background()
	svc := distill.New(e.db.SQL(), e.q, func(gen.ProviderConfig, string) propose.Model { return stubModel{text: "nothing"} })
	h := handlers.NewLessonDistillHandler(e.q, svc)
	r := chi.NewRouter()
	r.Mount("/", e.rr)
	r.Post("/lessons/{id}/dismiss", h.Dismiss)
	r.Post("/repos/{id}/lessons/distill", h.StartRun)
	r.Get("/repos/{id}/lessons/distill-runs", h.ListRuns)
	e.rr = r

	repoID := uuid.NewString()
	if _, err := e.q.CreateRepo(ctx, gen.CreateRepoParams{ID: repoID, Name: "r", Path: t.TempDir(), WorkflowID: &e.wfID}); err != nil {
		t.Fatal(err)
	}
	if w := e.do("POST", "/repos/missing/lessons/distill", ""); w.Code != 404 {
		t.Fatalf("missing repo: %d", w.Code)
	}
	if w := e.do("POST", "/repos/"+repoID+"/lessons/distill", ""); w.Code != 400 || !strings.Contains(w.Body.String(), "no provider config") {
		t.Fatalf("no provider: %d %s", w.Code, w.Body)
	}
	if w := e.do("GET", "/repos/"+repoID+"/lessons/distill-runs", ""); w.Code != 200 || strings.TrimSpace(w.Body.String()) != "[]" {
		t.Fatalf("runs: %d %s", w.Code, w.Body)
	}

	prop, _ := e.q.CreateRepoLesson(ctx, gen.CreateRepoLessonParams{ID: "p1", RepoID: &repoID, Kind: "trap", Body: "b", Citations: "[]", Source: "distiller", Status: "proposed"})
	human, _ := e.q.CreateRepoLesson(ctx, gen.CreateRepoLessonParams{ID: "h1", RepoID: &repoID, Kind: "trap", Body: "c", Citations: "[]", Source: "human", Status: "active"})
	if w := e.do("POST", "/lessons/"+human.ID+"/dismiss", ""); w.Code != 409 {
		t.Fatalf("dismiss human: %d", w.Code)
	}
	if w := e.do("POST", "/lessons/"+prop.ID+"/dismiss", ""); w.Code != 204 {
		t.Fatalf("dismiss: %d %s", w.Code, w.Body)
	}
	if w := e.do("POST", "/lessons/"+prop.ID+"/dismiss", ""); w.Code != 404 {
		t.Fatalf("dismiss again: %d", w.Code)
	}
}
