-- name: ListUserLLMKeys :many
SELECT * FROM llm_keys WHERE owner_id = sqlc.arg(owner_id) ORDER BY provider;

-- name: ListGlobalLLMKeys :many
SELECT * FROM llm_keys WHERE owner_id IS NULL ORDER BY provider;

-- name: GetUserLLMKey :one
SELECT * FROM llm_keys WHERE owner_id = sqlc.arg(owner_id) AND provider = sqlc.arg(provider);

-- name: GetGlobalLLMKey :one
SELECT * FROM llm_keys WHERE owner_id IS NULL AND provider = sqlc.arg(provider);

-- name: UpsertUserLLMKey :one
INSERT INTO llm_keys (owner_id, provider, plan, api_key_enc, updated_by)
VALUES (sqlc.arg(owner_id), sqlc.arg(provider), sqlc.arg(plan), sqlc.arg(api_key_enc), sqlc.arg(owner_id))
ON CONFLICT (owner_id, provider) WHERE owner_id IS NOT NULL DO UPDATE
SET plan = EXCLUDED.plan, api_key_enc = EXCLUDED.api_key_enc, updated_at = now(), updated_by = EXCLUDED.updated_by
RETURNING *;

-- name: UpsertGlobalLLMKey :one
INSERT INTO llm_keys (owner_id, provider, plan, api_key_enc, updated_by)
VALUES (NULL, sqlc.arg(provider), sqlc.arg(plan), sqlc.arg(api_key_enc), sqlc.arg(updated_by))
ON CONFLICT (provider) WHERE owner_id IS NULL DO UPDATE
SET plan = EXCLUDED.plan, api_key_enc = EXCLUDED.api_key_enc, updated_at = now(), updated_by = EXCLUDED.updated_by
RETURNING *;

-- name: DeleteUserLLMKey :execrows
DELETE FROM llm_keys WHERE owner_id = sqlc.arg(owner_id) AND provider = sqlc.arg(provider);

-- name: DeleteGlobalLLMKey :execrows
DELETE FROM llm_keys WHERE owner_id IS NULL AND provider = sqlc.arg(provider);

-- name: ListLLMKeysForVM :many
-- The broker's lookup for llm. As with ListIntegrationGrantsForVM, client_id
-- comes from the calling host's own token, so a host only ever names its own
-- vms. The owner's keys sort before the global ones.
SELECT k.id, k.provider, k.plan, k.api_key_enc, k.updated_at,
       (k.owner_id IS NULL)::boolean AS is_global,
       v.id AS vm_pk,
       v.name AS vm_name,
       v.created_by AS vm_owner
FROM vms v
JOIN llm_keys k ON k.owner_id = v.created_by OR k.owner_id IS NULL
WHERE v.client_id = sqlc.arg(client_id)
  AND v.ip = sqlc.arg(vm_ip)
ORDER BY is_global, k.provider;

-- name: UpsertLLMPrices :exec
INSERT INTO llm_prices (model, input_per_token, output_per_token, cache_read_per_token, cache_write_per_token, updated_at)
SELECT unnest(sqlc.arg(models)::text[]),
       unnest(sqlc.arg(inputs)::float8[]),
       unnest(sqlc.arg(outputs)::float8[]),
       unnest(sqlc.arg(cache_reads)::float8[]),
       unnest(sqlc.arg(cache_writes)::float8[]),
       now()
ON CONFLICT (model) DO UPDATE
SET input_per_token = EXCLUDED.input_per_token,
    output_per_token = EXCLUDED.output_per_token,
    cache_read_per_token = EXCLUDED.cache_read_per_token,
    cache_write_per_token = EXCLUDED.cache_write_per_token,
    updated_at = EXCLUDED.updated_at;

-- name: ListLLMPrices :many
SELECT * FROM llm_prices;

-- name: ListUsernamesByIDs :many
SELECT id, username FROM users WHERE id = ANY(sqlc.arg(ids)::uuid[]);
