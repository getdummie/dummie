-- name: InsertAuditLog :exec
INSERT INTO audit_logs (actor_id, actor_username, auth_method, action, path, params, status, ip, user_agent)
SELECT sqlc.narg(actor_id)::uuid,
       COALESCE(sqlc.narg(actor_username)::text, (SELECT username FROM users WHERE id = sqlc.narg(actor_id)::uuid), ''),
       sqlc.arg(auth_method)::text, sqlc.arg(action)::text, sqlc.arg(path)::text, sqlc.arg(params)::jsonb,
       sqlc.arg(status)::integer, sqlc.arg(ip)::text, sqlc.arg(user_agent)::text;

-- name: ListAuditLogs :many
SELECT * FROM audit_logs
WHERE (sqlc.arg(actor)::text = '' OR actor_username ILIKE '%' || sqlc.arg(actor)::text || '%')
  AND (sqlc.arg(action)::text = '' OR action ILIKE '%' || sqlc.arg(action)::text || '%')
  AND (NOT sqlc.arg(failed_only)::boolean OR status >= 400)
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg(row_limit) OFFSET sqlc.arg(row_offset);

-- name: CountAuditLogs :one
SELECT count(*) FROM audit_logs
WHERE (sqlc.arg(actor)::text = '' OR actor_username ILIKE '%' || sqlc.arg(actor)::text || '%')
  AND (sqlc.arg(action)::text = '' OR action ILIKE '%' || sqlc.arg(action)::text || '%')
  AND (NOT sqlc.arg(failed_only)::boolean OR status >= 400);

-- name: DeleteAuditLogsBefore :execrows
DELETE FROM audit_logs
WHERE created_at < now() - make_interval(days => sqlc.arg(days)::integer);
