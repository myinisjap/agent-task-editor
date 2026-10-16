DELETE FROM model_pricing WHERE model IN ('claude-opus-4-5');
ALTER TABLE model_pricing DROP COLUMN cache_write_input_per_1m;
