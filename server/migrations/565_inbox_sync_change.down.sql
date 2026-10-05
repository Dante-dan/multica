DROP TRIGGER IF EXISTS trg_issue_inbox_sync_delete ON issue;
DROP TRIGGER IF EXISTS trg_issue_inbox_sync_update ON issue;
DROP TRIGGER IF EXISTS trg_inbox_sync_change ON inbox_item;
DROP FUNCTION IF EXISTS record_issue_inbox_sync_change();
DROP FUNCTION IF EXISTS record_inbox_sync_change();
DROP TABLE IF EXISTS inbox_sync_change;
DROP TABLE IF EXISTS inbox_sync_prune_mark;
