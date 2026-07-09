package handlers_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/myinisjap/agent-task-editor/backend/internal/api/handlers"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

// TestBackupHandler_StreamsValidSnapshot verifies GET /backup returns a
// 200 application/octet-stream response whose body is a valid, independent
// SQLite database containing the seeded row, and that the temp snapshot file
// created during the handler run is cleaned up afterward.
func TestBackupHandler_StreamsValidSnapshot(t *testing.T) {
	db := openTestDB(t)
	q := gen.New(db.SQL())

	wfs, err := q.ListWorkflows(context.Background())
	if err != nil || len(wfs) == 0 {
		t.Fatalf("list workflows: %v", err)
	}
	wfID := wfs[0].ID

	repoID := uuid.NewString()
	if _, err := q.CreateRepo(context.Background(), gen.CreateRepoParams{
		ID:         repoID,
		Name:       "test-repo",
		Path:       t.TempDir(),
		WorkflowID: &wfID,
	}); err != nil {
		t.Fatalf("create repo: %v", err)
	}
	task, err := q.CreateTask(context.Background(), gen.CreateTaskParams{
		ID:          uuid.NewString(),
		Title:       "seeded task",
		Description: "",
		Type:        "feature",
		Label:       "not_ready",
		RepoID:      repoID,
		WorkflowID:  wfID,
		Attachments: "[]",
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}

	dbDir := filepath.Dir(db.Path())

	h := handlers.NewBackupHandler(db)
	req := httptest.NewRequest(http.MethodGet, "/backup", nil)
	w := httptest.NewRecorder()
	h.Backup(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/octet-stream" {
		t.Errorf("expected Content-Type application/octet-stream, got %q", ct)
	}
	if cd := w.Header().Get("Content-Disposition"); !strings.Contains(cd, "attachment") || !strings.Contains(cd, ".db") {
		t.Errorf("expected Content-Disposition attachment with .db filename, got %q", cd)
	}

	body := w.Body.Bytes()
	if len(body) < 16 || string(body[:16]) != "SQLite format 3\x00" {
		t.Fatalf("response body does not look like a SQLite file (len=%d)", len(body))
	}

	// Write the streamed bytes to a temp file and open it independently to
	// confirm the seeded row round-tripped through VACUUM INTO.
	snapshotPath := t.TempDir() + "/snapshot.db"
	if err := os.WriteFile(snapshotPath, body, 0o644); err != nil {
		t.Fatalf("write snapshot: %v", err)
	}
	snapshotDB, err := storage.Open(snapshotPath)
	if err != nil {
		t.Fatalf("open snapshot db: %v", err)
	}
	defer func() { _ = snapshotDB.Close() }()

	var title string
	if err := snapshotDB.SQL().QueryRow("SELECT title FROM tasks WHERE id = ?", task.ID).Scan(&title); err != nil {
		t.Fatalf("query snapshot: %v", err)
	}
	if title != "seeded task" {
		t.Errorf("expected seeded task title in snapshot, got %q", title)
	}

	// The handler's temp snapshot file must be cleaned up — no leftover
	// agent-task-editor-backup-*.db in the DB's directory.
	entries, err := os.ReadDir(dbDir)
	if err != nil {
		t.Fatalf("read db dir: %v", err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "agent-task-editor-backup-") {
			t.Errorf("leftover temp backup file not cleaned up: %s", e.Name())
		}
	}
}
