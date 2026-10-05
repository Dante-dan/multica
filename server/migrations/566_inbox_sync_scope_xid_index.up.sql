CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_inbox_sync_scope_xid ON inbox_sync_change (workspace_id, recipient_type, recipient_id, change_xid, item_id);
