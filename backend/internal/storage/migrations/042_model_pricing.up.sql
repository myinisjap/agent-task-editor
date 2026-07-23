-- User-editable token pricing table (see internal/agent/providers/pricing.go
-- and handlers.ModelPricingHandler). Seeded from the hardcoded modelPricing
-- map in pricing.go so existing estimates are unchanged on upgrade; that
-- hardcoded map remains a fallback (exact match, then longest-prefix match)
-- for any model not present here. Rows are looked up at run-completion time
-- (not cached at startup), so an edit here takes effect on the very next run
-- without a process restart.
CREATE TABLE model_pricing (
    model         TEXT PRIMARY KEY,
    input_per_1m  REAL NOT NULL,
    output_per_1m REAL NOT NULL,
    updated_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

INSERT INTO model_pricing (model, input_per_1m, output_per_1m) VALUES
    ('claude-opus-4',     15,   75),
    ('claude-opus-4-1',   15,   75),
    ('claude-sonnet-4',   3,    15),
    ('claude-sonnet-4-5', 3,    15),
    ('claude-sonnet-5',   3,    15),
    ('claude-haiku-4-5',  1,    5),
    ('claude-3-5-sonnet', 3,    15),
    ('claude-3-5-haiku',  0.8,  4),
    ('claude-3-opus',     15,   75),
    ('claude-3-haiku',    0.25, 1.25),
    ('gpt-4o',            2.5,  10),
    ('gpt-4o-mini',       0.15, 0.6),
    ('gpt-4.1',           2,    8),
    ('gpt-4.1-mini',      0.4,  1.6),
    ('gpt-4.1-nano',      0.1,  0.4),
    ('o3',                2,    8),
    ('o3-mini',           1.1,  4.4),
    ('o4-mini',           1.1,  4.4);

-- cost_unknown flags a run where tokens were consumed but no price (DB row
-- or hardcoded fallback) matched the model, so cost_usd was left at 0 as a
-- "we don't know" placeholder rather than a legitimate free run. Only ever
-- set by the anthropic/llm providers (see runAccumulators.attach); claude/
-- qwen_code report their own authoritative cost and never set this.
ALTER TABLE agent_runs ADD COLUMN cost_unknown INTEGER NOT NULL DEFAULT 0;
