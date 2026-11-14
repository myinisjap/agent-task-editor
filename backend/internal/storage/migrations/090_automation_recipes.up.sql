-- Automation recipes: objects created by installing a recipe (see
-- internal/recipes) carry the recipe id so the UI can show "from recipe" and
-- "Remove recipe" can delete exactly the tagged objects. '' = not from a recipe.
ALTER TABLE task_templates ADD COLUMN recipe_id TEXT NOT NULL DEFAULT '';
ALTER TABLE task_schedules ADD COLUMN recipe_id TEXT NOT NULL DEFAULT '';
ALTER TABLE intake_endpoints ADD COLUMN recipe_id TEXT NOT NULL DEFAULT '';
ALTER TABLE intake_rules ADD COLUMN recipe_id TEXT NOT NULL DEFAULT '';
