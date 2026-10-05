package handler

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

const inboxSyncLimit = 1000
const inboxSyncCursorMaxBytes = 8192

type inboxSyncCursor struct {
	Version     string `json:"v"`
	WorkspaceID string `json:"w"`
	UserID      string `json:"u"`
	Snapshot    string `json:"s"`
}

func encodeInboxSyncCursor(workspaceID, userID, snapshot string) string {
	raw, _ := json.Marshal(inboxSyncCursor{"inbox-1", workspaceID, userID, snapshot})
	cursor := base64.RawURLEncoding.EncodeToString(raw)
	if len(cursor) > inboxSyncCursorMaxBytes {
		return ""
	}
	return cursor
}

func decodeInboxSyncCursor(raw, workspaceID, userID string) (inboxSyncCursor, error) {
	var cursor inboxSyncCursor
	if len(raw) == 0 || len(raw) > inboxSyncCursorMaxBytes {
		return cursor, errors.New("invalid cursor")
	}
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return cursor, err
	}
	if err = json.Unmarshal(decoded, &cursor); err != nil {
		return cursor, err
	}
	if cursor.Version != "inbox-1" || cursor.WorkspaceID != workspaceID || cursor.UserID != userID || !validPGSnapshotText(cursor.Snapshot) {
		return cursor, errors.New("invalid cursor")
	}
	return cursor, nil
}

type InboxSyncResponse struct {
	Upserts []InboxItemResponse `json:"upserts"`
	Deletes []string            `json:"deletes"`
	Cursor  string              `json:"cursor"`
	Resync  bool                `json:"resync"`
}

// SyncFamily initially supports the active, unfiltered inbox family. Unlike
// realtime replay it compares materialized states and includes removal from the
// family (archive, recipient change and hard delete), not just socket events.
// Every SQL read shares one primary REPEATABLE READ snapshot. Compact dirty keys
// track transaction visibility, not timestamps or pre-commit sequence numbers.
func (h *Handler) SyncFamily(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := ctxWorkspaceID(r.Context())
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace id")
	if !ok {
		return
	}
	if r.URL.Query().Get("family") != "inbox" {
		writeError(w, http.StatusBadRequest, "unsupported sync family")
		return
	}
	// Recipients are members, matching GET /api/inbox. Membership is enforced
	// by the route middleware; cursors are also account/workspace scoped.
	resp := InboxSyncResponse{Upserts: []InboxItemResponse{}, Deletes: []string{}, Resync: true}
	cur, err := decodeInboxSyncCursor(r.URL.Query().Get("cursor"), workspaceID, userID)
	if err != nil {
		writeJSON(w, http.StatusOK, resp)
		return
	}
	ctx := r.Context()
	err = h.runSearchIndexRead(ctx, func(q *db.Queries) error {
		snapshot, err := q.GetSearchIndexCurrentSnapshot(ctx)
		if err != nil {
			return err
		}
		// A future cursor or a cursor from a reset database cannot prove coverage.
		sinceXmax, _ := strconv.ParseUint(strings.Split(cur.Snapshot, ":")[1], 10, 64)
		targetXmax, _ := strconv.ParseUint(strings.Split(snapshot, ":")[1], 10, 64)
		if sinceXmax > targetXmax {
			return nil
		}
		pruned, err := q.InboxSyncSinceIsPruned(ctx, cur.Snapshot)
		if err != nil || pruned {
			return err
		}
		ids, err := q.ListInboxSyncChanges(ctx, db.ListInboxSyncChangesParams{
			WorkspaceID: wsUUID, RecipientType: "member", RecipientID: parseUUID(userID),
			SinceSnapshot: cur.Snapshot, PageLimit: inboxSyncLimit + 1,
		})
		if err != nil {
			return err
		}
		// A bounded reply must never advance past omitted changes. The client
		// replaces this family from its full list when the delta is too large.
		if len(ids) > inboxSyncLimit {
			return nil
		}
		rows, err := q.ListInboxSyncItems(ctx, db.ListInboxSyncItemsParams{
			WorkspaceID: wsUUID, RecipientType: "member", RecipientID: parseUUID(userID), Ids: ids,
		})
		if err != nil {
			return err
		}
		live := make(map[pgtype.UUID]bool, len(rows))
		for _, row := range rows {
			resp.Upserts = append(resp.Upserts, inboxRowToResponse(db.ListInboxItemsRow(row)))
			live[row.ID] = true
		}
		for _, id := range ids {
			if !live[id] {
				resp.Deletes = append(resp.Deletes, uuidToString(id))
			}
		}
		resp.Cursor = encodeInboxSyncCursor(workspaceID, userID, snapshot)
		resp.Resync = resp.Cursor == ""
		return nil
	})
	if err != nil {
		h.writeSearchIndexError(w, r, "inbox sync", err)
		return
	}
	if resp.Resync {
		resp.Upserts = []InboxItemResponse{}
		resp.Deletes = []string{}
	}
	writeJSON(w, http.StatusOK, resp)
}
