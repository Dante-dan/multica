-- name: GetIssueTombstoneByID :one
SELECT id, workspace_id, number, deleted_at FROM issue_tombstone
WHERE workspace_id = $1 AND id = $2;

-- name: GetIssueTombstoneByNumber :one
SELECT id, workspace_id, number, deleted_at FROM issue_tombstone
WHERE workspace_id = $1 AND number = $2;

-- name: DeleteWorkspaceIssueTombstones :exec
DELETE FROM issue_tombstone WHERE workspace_id = $1;
