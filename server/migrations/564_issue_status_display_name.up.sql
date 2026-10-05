-- Workspace presentation only: the canonical name, key and workflow remain unchanged.
ALTER TABLE issue_status ADD COLUMN display_name TEXT NOT NULL DEFAULT ''
    CHECK (char_length(display_name) <= 64);
ALTER TABLE issue_status ADD CONSTRAINT issue_status_display_name_system_only
    CHECK (is_system OR display_name = '');
