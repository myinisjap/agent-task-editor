-- Intake endpoints: signed inbound webhooks that create tasks (see
-- internal/intakehook). `secret` is the HMAC-SHA256 key used to verify
-- X-ATE-Signature-256. It must be recoverable to verify, so it is stored in
-- plaintext (same approach as provider env secrets) and is only returned by
-- the API on create / rotate, never from GET or LIST.
CREATE TABLE intake_endpoints (
  id                   TEXT PRIMARY KEY,
  name                 TEXT NOT NULL,
  repo_id              TEXT NOT NULL REFERENCES repos(id) ON DELETE CASCADE,
  workflow_id          TEXT REFERENCES workflows(id) ON DELETE SET NULL,
  target_label         TEXT NOT NULL DEFAULT '',
  secret               TEXT NOT NULL,
  enabled              BOOLEAN NOT NULL DEFAULT 1,
  title_template       TEXT NOT NULL DEFAULT '',
  description_template  TEXT NOT NULL DEFAULT '',
  dedupe_key_template  TEXT NOT NULL DEFAULT '',
  last_received_at     DATETIME,
  created_at           DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at           DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_intake_endpoints_repo ON intake_endpoints(repo_id);
