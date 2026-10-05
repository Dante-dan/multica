package handler

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/testutil"
)

func inboxSyncWorkspace(t *testing.T) string {
	t.Helper()
	ws := searchIndexWorkspace(t, "INB")
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM inbox_sync_change WHERE workspace_id = $1`, ws)
	})
	dbfx.Member(t, ws, testUserID, "owner")
	return ws
}
func inboxSnapshotCursor(t *testing.T, ws string) string {
	t.Helper()
	response := testutil.Call(t, inboxWorkspaceHandler(testHandler.ListInbox), inboxRequest(http.MethodGet, "/api/inbox", ws)).Want(http.StatusOK)
	cursor := response.Header().Get("X-Sync-Cursor")
	if cursor == "" {
		t.Fatal("missing snapshot cursor")
	}
	return cursor
}
func inboxDelta(t *testing.T, ws, cursor string) InboxSyncResponse {
	t.Helper()
	return testutil.Decode[InboxSyncResponse](t, inboxWorkspaceHandler(testHandler.SyncFamily),
		inboxRequest(http.MethodGet, "/api/sync?family=inbox&cursor="+url.QueryEscape(cursor), ws), http.StatusOK)
}
func inboxSyncItem(t *testing.T, ws, title string, extra testutil.Cols) string {
	t.Helper()
	cols := testutil.Cols{"workspace_id": ws, "recipient_type": "member", "recipient_id": testUserID, "type": "status_changed", "severity": "info", "title": title}
	for k, v := range extra {
		cols[k] = v
	}
	return dbfx.Insert(t, "inbox_item", cols)
}
func TestInboxSyncLifecycleAndProjection(t *testing.T) {
	ws := inboxSyncWorkspace(t)
	issue := dbfx.Issue(t, "Joined issue", testutil.Cols{"workspace_id": ws})
	archived := inboxSyncItem(t, ws, "archive", nil)
	deleted := inboxSyncItem(t, ws, "delete", nil)
	projected := inboxSyncItem(t, ws, "projected", testutil.Cols{"issue_id": issue})
	cursor := inboxSnapshotCursor(t, ws)
	if d := inboxDelta(t, ws, cursor); d.Resync || len(d.Upserts) != 0 || len(d.Deletes) != 0 {
		t.Fatalf("unchanged: %+v", d)
	}
	dbfx.Exec(t, `UPDATE inbox_item SET archived = true WHERE id = $1`, archived)
	dbfx.Exec(t, `DELETE FROM inbox_item WHERE id = $1`, deleted)
	dbfx.Exec(t, `UPDATE issue SET priority = 'high' WHERE id = $1`, issue)
	d := inboxDelta(t, ws, cursor)
	if d.Resync || len(d.Upserts) != 1 || d.Upserts[0].ID != projected || d.Upserts[0].IssuePriority == nil || *d.Upserts[0].IssuePriority != "high" {
		t.Fatalf("projection: %+v", d)
	}
	assertIDs(t, "deletes", sortedIDs(d.Deletes...), []string{archived, deleted})
	dbfx.Exec(t, `UPDATE inbox_item SET archived = false WHERE id = $1`, archived)
	d = inboxDelta(t, ws, d.Cursor)
	if d.Resync || len(d.Upserts) != 1 || d.Upserts[0].ID != archived {
		t.Fatalf("unarchive: %+v", d)
	}
}
func TestInboxSyncCommitOrderAndScope(t *testing.T) {
	ws := inboxSyncWorkspace(t)
	ctx := context.Background()
	tx, err := testPool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	var pendingID string
	err = tx.QueryRow(ctx, `INSERT INTO inbox_item (workspace_id, recipient_type, recipient_id, type, severity, title) VALUES ($1, 'member', $2, 'status_changed', 'info', 'late') RETURNING id`, ws, testUserID).Scan(&pendingID)
	if err != nil {
		t.Fatal(err)
	}
	cursor := inboxSnapshotCursor(t, ws) // excludes the still-running transaction
	inboxSyncItem(t, ws, "later committed first", nil)
	first := inboxDelta(t, ws, cursor)
	if first.Resync || len(first.Upserts) != 1 {
		t.Fatalf("first: %+v", first)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { testPool.Exec(ctx, `DELETE FROM inbox_item WHERE id=$1`, pendingID) })
	second := inboxDelta(t, ws, first.Cursor)
	if second.Resync || len(second.Upserts) != 1 || second.Upserts[0].ID != pendingID {
		t.Fatalf("late commit lost: %+v", second)
	}
	// Changing recipient removes the id from the original cache without exposing
	// a different member's item or accepting another workspace's cursor.
	otherUser := dbfx.User(t, "other", "inbox-other-"+fmt.Sprint(time.Now().UnixNano())+"@example.com")
	dbfx.Exec(t, `UPDATE inbox_item SET recipient_id = $1 WHERE id = $2`, otherUser, pendingID)
	removed := inboxDelta(t, ws, second.Cursor)
	if removed.Resync || len(removed.Upserts) != 0 || len(removed.Deletes) != 1 || removed.Deletes[0] != pendingID {
		t.Fatalf("recipient change: %+v", removed)
	}
	otherWS := inboxSyncWorkspace(t)
	if !inboxDelta(t, otherWS, second.Cursor).Resync {
		t.Fatal("cross-workspace cursor accepted")
	}
	req := inboxRequest(http.MethodGet, "/api/sync?family=inbox&cursor="+url.QueryEscape(second.Cursor), ws)
	req.Header.Set("X-User-ID", otherUser)
	testutil.Call(t, inboxWorkspaceHandler(testHandler.SyncFamily), req).Want(http.StatusForbidden)
}
func TestInboxSyncFailsClosedAndBoundsDelta(t *testing.T) {
	ws := inboxSyncWorkspace(t)
	for _, cursor := range []string{"", "broken", encodeInboxSyncCursor(ws, testUserID, "18446744073709551614:18446744073709551615:")} {
		if d := inboxDelta(t, ws, cursor); !d.Resync || len(d.Upserts) != 0 || len(d.Deletes) != 0 {
			t.Fatalf("invalid cursor: %+v", d)
		}
	}
	cursor := inboxSnapshotCursor(t, ws)
	dbfx.Exec(t, `INSERT INTO inbox_item (workspace_id, recipient_type, recipient_id, type, severity, title) SELECT $1, 'member', $2, 'status_changed', 'info', 'many' FROM generate_series(1, 1001)`, ws, testUserID)
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM inbox_item WHERE workspace_id=$1`, ws) })
	if d := inboxDelta(t, ws, cursor); !d.Resync || d.Cursor != "" || len(d.Upserts) != 0 {
		t.Fatalf("overflow: %+v", d)
	}
	cursor = inboxSnapshotCursor(t, ws)
	snapshot, err := decodeInboxSyncCursor(cursor, ws, testUserID)
	if err != nil {
		t.Fatal(err)
	}
	// A pruned xid potentially unseen by this snapshot requires full rebuild.
	dbfx.Exec(t, `INSERT INTO inbox_sync_prune_mark (singleton, pruned_through_xid) VALUES (true, pg_snapshot_xmin($1::pg_snapshot)) ON CONFLICT(singleton) DO UPDATE SET pruned_through_xid=EXCLUDED.pruned_through_xid`, snapshot.Snapshot)
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM inbox_sync_prune_mark`) })
	if !inboxDelta(t, ws, cursor).Resync {
		t.Fatal("pruned cursor accepted")
	}
}
