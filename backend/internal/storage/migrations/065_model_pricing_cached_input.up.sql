-- Optional USD-per-1M price for cached (prompt-cache hit) input tokens.
-- NULL means "bill cached tokens at input_per_1m", which is how every row was
-- priced before this column existed. Used by the codex_cli provider, whose
-- turn.completed usage reports cached_input_tokens as a subset of
-- input_tokens; long agent runs are mostly cache hits, so pricing them at the
-- full input rate overstated cost several times over.
ALTER TABLE model_pricing ADD COLUMN cached_input_per_1m REAL;

-- Fill the cached rate for the seeded OpenAI rows (published prices), only
-- where the row still carries its seeded input price so a user's own edit is
-- left alone.
UPDATE model_pricing SET cached_input_per_1m = 1.25  WHERE model = 'gpt-4o'       AND input_per_1m = 2.5;
UPDATE model_pricing SET cached_input_per_1m = 0.075 WHERE model = 'gpt-4o-mini'  AND input_per_1m = 0.15;
UPDATE model_pricing SET cached_input_per_1m = 0.5   WHERE model = 'gpt-4.1'      AND input_per_1m = 2;
UPDATE model_pricing SET cached_input_per_1m = 0.1   WHERE model = 'gpt-4.1-mini' AND input_per_1m = 0.4;
UPDATE model_pricing SET cached_input_per_1m = 0.025 WHERE model = 'gpt-4.1-nano' AND input_per_1m = 0.1;
UPDATE model_pricing SET cached_input_per_1m = 0.5   WHERE model = 'o3'           AND input_per_1m = 2;
UPDATE model_pricing SET cached_input_per_1m = 0.55  WHERE model = 'o3-mini'      AND input_per_1m = 1.1;
UPDATE model_pricing SET cached_input_per_1m = 0.275 WHERE model = 'o4-mini'      AND input_per_1m = 1.1;
