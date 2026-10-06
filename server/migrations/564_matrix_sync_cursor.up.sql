-- The supervisor rebuilds adapters on reconnect. Keep a per-device sync cursor
-- so reconnects do not baseline away messages sent while the bot was offline.
-- No foreign keys: installation cleanup remains an application responsibility.
CREATE TABLE channel_matrix_sync_cursor (
    installation_id UUID NOT NULL,
    app_id TEXT NOT NULL,
    device_id TEXT NOT NULL,
    sync_token TEXT NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (installation_id, app_id, device_id)
);

ALTER TABLE issue DROP CONSTRAINT issue_origin_type_check;
ALTER TABLE issue ADD CONSTRAINT issue_origin_type_check
    CHECK (origin_type IN ('autopilot', 'quick_create', 'lark_chat', 'slack_chat', 'agent_create', 'dingtalk_chat', 'wecom_chat', 'telegram_chat', 'matrix_chat'))
    NOT VALID;
