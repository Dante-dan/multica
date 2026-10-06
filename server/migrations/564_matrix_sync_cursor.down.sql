-- Refuse rollback while Matrix-origin issues remain rather than inventing a
-- different source for existing records.
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM issue WHERE origin_type = 'matrix_chat') THEN
        RAISE EXCEPTION 'cannot remove Matrix origin while Matrix issues remain';
    END IF;
END $$;
ALTER TABLE issue DROP CONSTRAINT issue_origin_type_check;
ALTER TABLE issue ADD CONSTRAINT issue_origin_type_check
    CHECK (origin_type IN ('autopilot', 'quick_create', 'lark_chat', 'slack_chat', 'agent_create', 'dingtalk_chat', 'wecom_chat', 'telegram_chat'))
    NOT VALID;
DROP TABLE channel_matrix_sync_cursor;
