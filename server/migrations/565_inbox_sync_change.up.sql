-- Per-recipient materialized-state catch-up, independent of realtime replay.
-- Compact to each id's last xid (including hard deletes). pg_snapshot visibility
-- handles late commits exactly, without globally serializing writers.
SET LOCAL lock_timeout = '2s';
SET LOCAL statement_timeout = '10s';
CREATE TABLE IF NOT EXISTS inbox_sync_change (
    workspace_id UUID NOT NULL,
    recipient_type TEXT NOT NULL,
    recipient_id UUID NOT NULL,
    item_id UUID NOT NULL,
    change_xid xid8 NOT NULL,
    changed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (workspace_id, recipient_type, recipient_id, item_id)
);
CREATE TABLE IF NOT EXISTS inbox_sync_prune_mark (
    singleton BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK (singleton),
    pruned_through_xid xid8 NOT NULL
);
CREATE OR REPLACE FUNCTION record_inbox_sync_change()
RETURNS TRIGGER LANGUAGE plpgsql AS $function$
BEGIN
    -- Record both scopes when an item changes workspace or recipient: the old
    -- family must remove it, not silently retain it forever.
    IF TG_OP = 'DELETE' OR (TG_OP = 'UPDATE' AND
       (OLD.workspace_id, OLD.recipient_type, OLD.recipient_id) IS DISTINCT FROM
       (NEW.workspace_id, NEW.recipient_type, NEW.recipient_id)) THEN
        INSERT INTO inbox_sync_change
        VALUES (OLD.workspace_id, OLD.recipient_type, OLD.recipient_id, OLD.id, pg_current_xact_id(), now())
        ON CONFLICT (workspace_id, recipient_type, recipient_id, item_id) DO UPDATE
        SET change_xid = EXCLUDED.change_xid, changed_at = EXCLUDED.changed_at;
    END IF;
    IF TG_OP IN ('INSERT', 'UPDATE') THEN
        INSERT INTO inbox_sync_change
        VALUES (NEW.workspace_id, NEW.recipient_type, NEW.recipient_id, NEW.id, pg_current_xact_id(), now())
        ON CONFLICT (workspace_id, recipient_type, recipient_id, item_id) DO UPDATE
        SET change_xid = EXCLUDED.change_xid, changed_at = EXCLUDED.changed_at;
    END IF;
    RETURN NULL;
END
$function$;
DROP TRIGGER IF EXISTS trg_inbox_sync_change ON inbox_item;
CREATE TRIGGER trg_inbox_sync_change
AFTER INSERT OR UPDATE OR DELETE ON inbox_item FOR EACH ROW
WHEN (current_setting('multica.workspace_teardown', true) IS DISTINCT FROM 'on')
EXECUTE FUNCTION record_inbox_sync_change();

-- The inbox DTO joins issue.status and issue.priority. An issue write can thus
-- change a list row without changing the notification itself. Deleting an issue
-- also changes those projections, even if another write path keeps the inbox.
CREATE OR REPLACE FUNCTION record_issue_inbox_sync_change()
RETURNS TRIGGER LANGUAGE plpgsql AS $function$
BEGIN
    INSERT INTO inbox_sync_change
    SELECT workspace_id, recipient_type, recipient_id, id, pg_current_xact_id(), now()
    FROM inbox_item WHERE issue_id = OLD.id
    ORDER BY workspace_id, recipient_type, recipient_id, id
    ON CONFLICT (workspace_id, recipient_type, recipient_id, item_id) DO UPDATE
    SET change_xid = EXCLUDED.change_xid, changed_at = EXCLUDED.changed_at;
    RETURN NULL;
END
$function$;
DROP TRIGGER IF EXISTS trg_issue_inbox_sync_update ON issue;
CREATE TRIGGER trg_issue_inbox_sync_update
AFTER UPDATE OF status, priority ON issue FOR EACH ROW
WHEN (current_setting('multica.workspace_teardown', true) IS DISTINCT FROM 'on')
EXECUTE FUNCTION record_issue_inbox_sync_change();
DROP TRIGGER IF EXISTS trg_issue_inbox_sync_delete ON issue;
CREATE TRIGGER trg_issue_inbox_sync_delete
AFTER DELETE ON issue FOR EACH ROW
WHEN (current_setting('multica.workspace_teardown', true) IS DISTINCT FROM 'on')
EXECUTE FUNCTION record_issue_inbox_sync_change();
