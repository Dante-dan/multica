CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS issue_tombstone_workspace_number_idx ON issue_tombstone (workspace_id, number);
