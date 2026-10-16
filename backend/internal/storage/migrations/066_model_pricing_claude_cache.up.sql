-- Optional USD-per-1M price for cache *write* tokens (claude/qwen's
-- cache_creation_input_tokens — writing a prompt prefix into the cache for
-- the first time). NULL means "bill cache-write tokens at input_per_1m",
-- which is how every row was priced before this column existed (and
-- remains the behavior for any model/provider that doesn't report a
-- cache-write counter, e.g. codex_cli). See cached_input_per_1m (065) for
-- the analogous cache-*read* rate.
ALTER TABLE model_pricing ADD COLUMN cache_write_input_per_1m REAL;

-- Backfill the cached-read rate for the Claude rows seeded in 042 that are
-- still at their seeded input price (an operator's own edit is left alone),
-- and add the missing newer model IDs + their cache-write rate. Anthropic's
-- published pricing uses a consistent 0.1x-of-input cache-read / 1.25x-of-
-- input cache-write multiplier across every Claude generation — see
-- pricing.go's modelPricing map for the per-model verified source values
-- this mirrors.
UPDATE model_pricing SET cached_input_per_1m = 1.50, cache_write_input_per_1m = 18.75 WHERE model = 'claude-opus-4'     AND input_per_1m = 15;
UPDATE model_pricing SET cached_input_per_1m = 1.50, cache_write_input_per_1m = 18.75 WHERE model = 'claude-opus-4-1'   AND input_per_1m = 15;
UPDATE model_pricing SET cached_input_per_1m = 0.30, cache_write_input_per_1m = 3.75  WHERE model = 'claude-sonnet-4'   AND input_per_1m = 3;
UPDATE model_pricing SET cached_input_per_1m = 0.30, cache_write_input_per_1m = 3.75  WHERE model = 'claude-sonnet-4-5' AND input_per_1m = 3;
UPDATE model_pricing SET cached_input_per_1m = 0.30, cache_write_input_per_1m = 3.75  WHERE model = 'claude-sonnet-5'   AND input_per_1m = 3;
UPDATE model_pricing SET cached_input_per_1m = 0.10, cache_write_input_per_1m = 1.25  WHERE model = 'claude-haiku-4-5'  AND input_per_1m = 1;
UPDATE model_pricing SET cached_input_per_1m = 0.30, cache_write_input_per_1m = 3.75  WHERE model = 'claude-3-5-sonnet' AND input_per_1m = 3;
UPDATE model_pricing SET cached_input_per_1m = 0.08, cache_write_input_per_1m = 1     WHERE model = 'claude-3-5-haiku'  AND input_per_1m = 0.8;
UPDATE model_pricing SET cached_input_per_1m = 1.50, cache_write_input_per_1m = 18.75 WHERE model = 'claude-3-opus'     AND input_per_1m = 15;
UPDATE model_pricing SET cached_input_per_1m = 0.03, cache_write_input_per_1m = 0.3125 WHERE model = 'claude-3-haiku'    AND input_per_1m = 0.25;

-- New Claude model IDs shipped since 042/065 were seeded, with their
-- published pricing (including cache read/write rates). We deliberately
-- only add rows for model IDs with a verifiable published price — see
-- pricing.go's modelPricing map doc comment.
INSERT INTO model_pricing (model, input_per_1m, output_per_1m, cached_input_per_1m, cache_write_input_per_1m) VALUES
    ('claude-opus-4-5',   5,    25,   0.50, 6.25)
ON CONFLICT (model) DO NOTHING;
