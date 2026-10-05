-- name: InboxSyncSinceIsPruned :one
SELECT EXISTS (SELECT 1 FROM inbox_sync_prune_mark
    WHERE singleton AND pruned_through_xid >= pg_snapshot_xmin((@since_snapshot::text)::pg_snapshot)) AS pruned;

-- name: ListInboxSyncChanges :many
SELECT item_id FROM inbox_sync_change
WHERE workspace_id = @workspace_id AND recipient_type = @recipient_type AND recipient_id = @recipient_id
  AND change_xid >= pg_snapshot_xmin((@since_snapshot::text)::pg_snapshot)
  AND NOT pg_visible_in_snapshot(change_xid, (@since_snapshot::text)::pg_snapshot)
ORDER BY change_xid, item_id
LIMIT @page_limit;

-- name: ListInboxSyncItems :many
SELECT i.*, iss.status AS issue_status, iss.priority AS issue_priority
FROM inbox_item i LEFT JOIN issue iss ON iss.id = i.issue_id
WHERE i.workspace_id = @workspace_id AND i.recipient_type = @recipient_type AND i.recipient_id = @recipient_id
  AND i.archived = false AND i.id = ANY(@ids::uuid[])
ORDER BY i.created_at DESC;

-- name: DeleteWorkspaceInboxSyncChanges :exec
DELETE FROM inbox_sync_change WHERE workspace_id = $1;

-- name: PruneInboxSyncChanges :one
-- Deletes one bounded batch of rows older than the cutoff and raises the prune
-- mark to the highest xid removed, in the same statement.
WITH doomed AS (
    SELECT sic.workspace_id, sic.recipient_type, sic.recipient_id, sic.item_id
    FROM inbox_sync_change sic
    WHERE sic.changed_at < @cutoff
    ORDER BY sic.changed_at
    LIMIT @batch_size
    FOR UPDATE SKIP LOCKED
), deleted AS (
    DELETE FROM inbox_sync_change c
    USING doomed d
    WHERE c.workspace_id = d.workspace_id AND c.recipient_type = d.recipient_type
      AND c.recipient_id = d.recipient_id AND c.item_id = d.item_id
      AND c.changed_at < @cutoff
    RETURNING c.change_xid
), marked AS (
    INSERT INTO inbox_sync_prune_mark (singleton, pruned_through_xid)
    SELECT TRUE, deleted.change_xid FROM deleted ORDER BY deleted.change_xid DESC LIMIT 1
    ON CONFLICT (singleton) DO UPDATE
        SET pruned_through_xid = GREATEST(inbox_sync_prune_mark.pruned_through_xid, EXCLUDED.pruned_through_xid)
    RETURNING 1
)
SELECT count(*)::bigint AS deleted_count FROM deleted;

