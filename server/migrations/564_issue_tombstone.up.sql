-- Preserve only identity after deletion; issue content and related cleanup remain deleted.
-- Historical hard deletes cannot be reconstructed from gaps in issue numbers.
CREATE TABLE issue_tombstone (
    id UUID NOT NULL,
    workspace_id UUID NOT NULL,
    number INTEGER NOT NULL,
    deleted_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
