-- Automation recipes: tag/untag the objects a recipe install creates. See
-- migration 088 and internal/recipes. recipe_id '' = not from a recipe.

-- name: SetTaskTemplateRecipe :exec
UPDATE task_templates SET recipe_id = ? WHERE id = ?;

-- name: SetTaskScheduleRecipe :exec
UPDATE task_schedules SET recipe_id = ? WHERE id = ?;

-- name: SetIntakeEndpointRecipe :exec
UPDATE intake_endpoints SET recipe_id = ? WHERE id = ?;

-- name: SetIntakeRuleRecipe :exec
UPDATE intake_rules SET recipe_id = ? WHERE id = ?;

-- name: ListIntakeEndpointsByRecipe :many
SELECT * FROM intake_endpoints WHERE recipe_id = ? ORDER BY created_at, id;

-- name: DeleteIntakeRulesByRecipe :exec
DELETE FROM intake_rules WHERE recipe_id = ?;

-- name: DeleteTaskSchedulesByRecipe :exec
DELETE FROM task_schedules WHERE recipe_id = ?;

-- name: DeleteIntakeEndpointsByRecipe :exec
DELETE FROM intake_endpoints WHERE recipe_id = ?;

-- name: DeleteTaskTemplatesByRecipe :exec
DELETE FROM task_templates WHERE recipe_id = ?;
