-- name: CreateRecipe :one
INSERT INTO recipes (owner_id, name, description, config)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: ListRecipesByOwner :many
SELECT * FROM recipes
WHERE owner_id = $1
ORDER BY name;

-- name: GetRecipeForOwner :one
SELECT * FROM recipes
WHERE id = $1 AND owner_id = $2;

-- name: UpdateRecipeForOwner :one
UPDATE recipes
SET name        = $3,
    description = $4,
    config      = $5,
    updated_at  = now()
WHERE id = $1 AND owner_id = $2
RETURNING *;

-- name: DeleteRecipeForOwner :execrows
DELETE FROM recipes
WHERE id = $1 AND owner_id = $2;
