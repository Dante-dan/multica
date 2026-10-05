package handler

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// serveIssueTombstone is GET-only: a deleted identity never authorizes writes,
// appears in lists, or retains the deleted issue's content.
func (h *Handler) serveIssueTombstone(w http.ResponseWriter, r *http.Request, id string) bool {
	if _, ok := requireUserID(w, r); !ok {
		return true
	}
	workspaceID := h.resolveWorkspaceID(r)
	if workspaceID == "" {
		writeError(w, http.StatusBadRequest, "workspace_id is required")
		return true
	}
	wsUUID, err := util.ParseUUID(workspaceID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid workspace_id")
		return true
	}
	var row db.IssueTombstone
	if parts := splitIdentifier(id); parts != nil {
		prefix := h.getIssuePrefix(r.Context(), wsUUID)
		if prefix == "" || !strings.EqualFold(prefix, parts.prefix) {
			return false
		}
		row, err = h.Queries.GetIssueTombstoneByNumber(r.Context(), db.GetIssueTombstoneByNumberParams{WorkspaceID: wsUUID, Number: parts.number})
	} else {
		issueID, parseErr := util.ParseUUID(id)
		if parseErr != nil {
			return false
		}
		row, err = h.Queries.GetIssueTombstoneByID(r.Context(), db.GetIssueTombstoneByIDParams{WorkspaceID: wsUUID, ID: issueID})
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return false
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load issue deletion state")
		return true
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id":         uuidToString(row.ID),
		"identifier": h.getIssuePrefix(r.Context(), wsUUID) + "-" + strconv.FormatInt(int64(row.Number), 10),
		"number":     row.Number,
		"status":     "deleted",
		"deleted_at": row.DeletedAt.Time.UTC().Format(time.RFC3339Nano),
	})
	return true
}
