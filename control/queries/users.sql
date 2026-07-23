-- name: CreateUser :one
INSERT INTO users (username, email, password_hash, first_name, last_name)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: GetUserByUsernameOrEmail :one
SELECT * FROM users
WHERE username = $1 OR email = $1
LIMIT 1;

-- name: GetUserByID :one
SELECT * FROM users
WHERE id = $1
LIMIT 1;
