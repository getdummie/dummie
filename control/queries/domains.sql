-- name: ListDomains :many
SELECT * FROM domains
ORDER BY tld;

-- name: CountDomains :one
SELECT count(*) FROM domains;

-- name: CreateDomain :one
INSERT INTO domains (tld)
VALUES ($1)
RETURNING *;

-- name: DeleteDomain :exec
DELETE FROM domains
WHERE id = $1;

-- name: GetSoleDomain :one
-- GetSoleDomain returns the single configured domain, and no rows when there is
-- none or more than one. Enrollment uses it to pick a domain for a new agent
-- without having to guess: with exactly one there is nothing to choose, and with
-- several the choice is the operator's, so the agent is left unassigned.
SELECT * FROM domains
WHERE (SELECT count(*) FROM domains) = 1;
