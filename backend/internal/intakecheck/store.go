package intakecheck

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// Check statuses.
const (
	StatusRunning = "running"
	StatusDone    = "done"
	StatusFailed  = "failed"
)

// Verdicts.
const (
	VerdictReady       = "ready"
	VerdictNeedsDetail = "needs_detail"
)

// Settings is the singleton assistant configuration. Off by default.
type Settings struct {
	Enabled          bool    `json:"enabled"`
	AutoCheck        bool    `json:"auto_check"`
	ProviderConfigID *string `json:"provider_config_id"`
}

// Check is the stored result of one assistant run.
type Check struct {
	TaskID              string     `json:"task_id"`
	Status              string     `json:"status"`
	Trigger             string     `json:"trigger"`
	Verdict             string     `json:"verdict"`
	Questions           []string   `json:"questions"`
	SuggestedType       string     `json:"suggested_type"`
	SuggestedWorkflowID string     `json:"suggested_workflow_id"`
	DuplicateIDs        []string   `json:"duplicate_ids"`
	Error               string     `json:"error"`
	ProviderConfigID    *string    `json:"provider_config_id"`
	CostUSD             float64    `json:"cost_usd"`
	InputTokens         int64      `json:"input_tokens"`
	OutputTokens        int64      `json:"output_tokens"`
	StartedAt           time.Time  `json:"started_at"`
	FinishedAt          *time.Time `json:"finished_at"`
}

// Store is the raw-SQL persistence for settings and checks.
type Store struct{ DB *sql.DB }

// ErrNotFound means no check exists for the task.
var ErrNotFound = errors.New("not found")

// GetSettings reads the settings row (defaults when missing).
func (s Store) GetSettings(ctx context.Context) (Settings, error) {
	var en, auto int
	var pc sql.NullString
	err := s.DB.QueryRowContext(ctx, `SELECT enabled, auto_check, provider_config_id FROM intake_assistant_settings WHERE id = 1`).Scan(&en, &auto, &pc)
	if errors.Is(err, sql.ErrNoRows) {
		return Settings{AutoCheck: true}, nil
	}
	if err != nil {
		return Settings{}, err
	}
	out := Settings{Enabled: en != 0, AutoCheck: auto != 0}
	if pc.Valid && pc.String != "" {
		v := pc.String
		out.ProviderConfigID = &v
	}
	return out, nil
}

// PutSettings stores the settings row.
func (s Store) PutSettings(ctx context.Context, in Settings) error {
	var pc any
	if in.ProviderConfigID != nil && *in.ProviderConfigID != "" {
		pc = *in.ProviderConfigID
	}
	_, err := s.DB.ExecContext(ctx, `INSERT INTO intake_assistant_settings (id, enabled, auto_check, provider_config_id, updated_at)
VALUES (1, ?, ?, ?, CURRENT_TIMESTAMP)
ON CONFLICT (id) DO UPDATE SET enabled = excluded.enabled, auto_check = excluded.auto_check,
  provider_config_id = excluded.provider_config_id, updated_at = CURRENT_TIMESTAMP`, b2i(in.Enabled), b2i(in.AutoCheck), pc)
	return err
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// OpenCandidates lists the open (non-terminal), non-archived tasks of a repo,
// newest first, capped; excludeID is left out.
func (s Store) OpenCandidates(ctx context.Context, repoID, excludeID string, limit int) ([]Doc, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT t.id, t.title, t.description, t.label FROM tasks t
LEFT JOIN workflow_labels wl ON wl.workflow_id = t.workflow_id AND wl.name = t.label
WHERE t.repo_id = ? AND t.archived = 0 AND t.id != ? AND COALESCE(wl.is_terminal, 0) = 0
ORDER BY t.created_at DESC, t.id DESC LIMIT ?`, repoID, excludeID, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Doc
	for rows.Next() {
		var d Doc
		if err := rows.Scan(&d.ID, &d.Title, &d.Description, &d.Label); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

const checkCols = `task_id, status, trigger, verdict, questions, suggested_type, suggested_workflow_id, duplicate_ids,
error, provider_config_id, cost_usd, input_tokens, output_tokens, started_at, finished_at`

func scanCheck(sc interface{ Scan(...any) error }) (Check, error) {
	var c Check
	var q, d string
	var pc sql.NullString
	var fin sql.NullTime
	if err := sc.Scan(&c.TaskID, &c.Status, &c.Trigger, &c.Verdict, &q, &c.SuggestedType, &c.SuggestedWorkflowID, &d,
		&c.Error, &pc, &c.CostUSD, &c.InputTokens, &c.OutputTokens, &c.StartedAt, &fin); err != nil {
		return Check{}, err
	}
	_ = json.Unmarshal([]byte(q), &c.Questions)
	_ = json.Unmarshal([]byte(d), &c.DuplicateIDs)
	if c.Questions == nil {
		c.Questions = []string{}
	}
	if c.DuplicateIDs == nil {
		c.DuplicateIDs = []string{}
	}
	if pc.Valid {
		c.ProviderConfigID = &pc.String
	}
	if fin.Valid {
		c.FinishedAt = &fin.Time
	}
	return c, nil
}

// GetCheck returns the task's latest check.
func (s Store) GetCheck(ctx context.Context, taskID string) (Check, error) {
	c, err := scanCheck(s.DB.QueryRowContext(ctx, `SELECT `+checkCols+` FROM task_intake_checks WHERE task_id = ?`, taskID))
	if errors.Is(err, sql.ErrNoRows) {
		return Check{}, ErrNotFound
	}
	return c, err
}

// SaveCheck upserts the task's check row.
func (s Store) SaveCheck(ctx context.Context, c Check) error {
	q, _ := json.Marshal(nonNil(c.Questions))
	d, _ := json.Marshal(nonNil(c.DuplicateIDs))
	var pc any
	if c.ProviderConfigID != nil {
		pc = *c.ProviderConfigID
	}
	var fin any
	if c.FinishedAt != nil {
		fin = *c.FinishedAt
	}
	_, err := s.DB.ExecContext(ctx, `INSERT INTO task_intake_checks (`+checkCols+`)
VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT (task_id) DO UPDATE SET status=excluded.status, trigger=excluded.trigger, verdict=excluded.verdict,
 questions=excluded.questions, suggested_type=excluded.suggested_type, suggested_workflow_id=excluded.suggested_workflow_id,
 duplicate_ids=excluded.duplicate_ids, error=excluded.error, provider_config_id=excluded.provider_config_id,
 cost_usd=excluded.cost_usd, input_tokens=excluded.input_tokens, output_tokens=excluded.output_tokens,
 started_at=excluded.started_at, finished_at=excluded.finished_at`,
		c.TaskID, c.Status, c.Trigger, c.Verdict, string(q), c.SuggestedType, c.SuggestedWorkflowID, string(d),
		c.Error, pc, c.CostUSD, c.InputTokens, c.OutputTokens, c.StartedAt, fin)
	return err
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// Verdicts returns the finished verdict per task id, for the ids that have one.
func (s Store) Verdicts(ctx context.Context, ids []string) (map[string]string, error) {
	out := map[string]string{}
	if len(ids) == 0 {
		return out, nil
	}
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT task_id, verdict FROM task_intake_checks WHERE status = 'done' AND task_id IN (`+
		strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")+`)`, args...)
	if err != nil {
		return out, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id, v string
		if err := rows.Scan(&id, &v); err != nil {
			return out, err
		}
		out[id] = v
	}
	return out, rows.Err()
}

// UncheckedParked lists recent tasks sitting on an agent_ignore (parked) label
// that have no check yet, oldest first.
func (s Store) UncheckedParked(ctx context.Context, since time.Time, limit int) ([]string, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT t.id FROM tasks t
JOIN workflow_labels wl ON wl.workflow_id = t.workflow_id AND wl.name = t.label
WHERE wl.agent_ignore = 1 AND wl.is_terminal = 0 AND t.archived = 0 AND t.created_at >= ?
AND NOT EXISTS (SELECT 1 FROM task_intake_checks c WHERE c.task_id = t.id)
ORDER BY t.created_at ASC LIMIT ?`, since, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
