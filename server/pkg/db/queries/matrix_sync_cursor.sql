-- name: GetMatrixSyncCursor :one
SELECT sync_token FROM channel_matrix_sync_cursor
WHERE installation_id = $1 AND app_id = $2 AND device_id = $3;

-- name: SaveMatrixSyncCursor :exec
INSERT INTO channel_matrix_sync_cursor (installation_id, app_id, device_id, sync_token)
VALUES ($1, $2, $3, $4)
ON CONFLICT (installation_id, app_id, device_id) DO UPDATE
SET sync_token = EXCLUDED.sync_token, updated_at = now();

-- name: DeleteMatrixSyncCursors :exec
DELETE FROM channel_matrix_sync_cursor WHERE installation_id = $1;
