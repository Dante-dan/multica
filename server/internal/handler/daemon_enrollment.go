package handler

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/auth"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// These handlers implement the administrator side of single-workspace host
// enrollment. Route activation requires the daemon credential/authentication
// lifecycle and WebSocket revocation fence; this file alone is not that feature.
func (h *Handler) enrollmentAdministrator(w http.ResponseWriter, r *http.Request) (db.Member, bool) {
	wsID := workspaceIDFromURL(r, "id")
	member, ok := h.requireWorkspaceMember(w, r, wsID, "workspace not found")
	if !ok {
		return db.Member{}, false
	}
	if !roleAllowed(member.Role, "owner", "admin") {
		writeError(w, http.StatusForbidden, "insufficient permissions")
		return db.Member{}, false
	}
	return member, true
}

// enrollmentProjection never exposes token_hash, even to administrators.
func enrollmentProjection(e db.DaemonEnrollment) map[string]any {
	return map[string]any{
		"id": uuidToString(e.ID), "workspace_id": uuidToString(e.WorkspaceID),
		"daemon_id": e.DaemonID, "name": e.Name, "creator_id": uuidToString(e.CreatorID),
		"generation": e.Generation, "expires_at": e.ExpiresAt.Time,
		"revoked": e.RevokedAt.Valid, "created_at": e.CreatedAt.Time,
	}
}

func (h *Handler) CreateDaemonEnrollment(w http.ResponseWriter, r *http.Request) {
	member, ok := h.enrollmentAdministrator(w, r)
	if !ok {
		return
	}
	var req struct {
		DaemonID string `json:"daemon_id"`
		Name     string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.DaemonID = strings.TrimSpace(req.DaemonID)
	req.Name = strings.TrimSpace(req.Name)
	if req.DaemonID == "" || req.Name == "" {
		writeError(w, http.StatusBadRequest, "daemon_id and name are required")
		return
	}
	raw, err := auth.GenerateDaemonEnrollmentToken()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to generate credential")
		return
	}
	e, err := h.Queries.CreateDaemonEnrollment(r.Context(), db.CreateDaemonEnrollmentParams{
		WorkspaceID: member.WorkspaceID, DaemonID: req.DaemonID, Name: req.Name,
		CreatorID: member.UserID, TokenHash: auth.HashToken(raw),
		ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(30 * 24 * time.Hour), Valid: true},
	})
	if isUniqueViolation(err) {
		writeError(w, http.StatusConflict, "daemon is already enrolled in this workspace")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create enrollment")
		return
	}
	result := enrollmentProjection(e)
	result["token"] = raw
	writeJSON(w, http.StatusCreated, result)
}

func (h *Handler) ListDaemonEnrollments(w http.ResponseWriter, r *http.Request) {
	member, ok := h.enrollmentAdministrator(w, r)
	if !ok {
		return
	}
	rows, err := h.Queries.ListDaemonEnrollments(r.Context(), member.WorkspaceID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list enrollments")
		return
	}
	result := make([]map[string]any, 0, len(rows))
	for _, e := range rows {
		result = append(result, enrollmentProjection(e))
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *Handler) RotateDaemonEnrollment(w http.ResponseWriter, r *http.Request) {
	member, ok := h.enrollmentAdministrator(w, r)
	if !ok {
		return
	}
	id, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "enrollmentId"), "enrollment_id")
	if !ok {
		return
	}
	raw, err := auth.GenerateDaemonEnrollmentToken()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to generate credential")
		return
	}
	e, err := h.Queries.RotateDaemonEnrollment(r.Context(), db.RotateDaemonEnrollmentParams{
		ID: id, WorkspaceID: member.WorkspaceID, TokenHash: auth.HashToken(raw),
		ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(30 * 24 * time.Hour), Valid: true},
	})
	if isNotFound(err) {
		writeError(w, http.StatusNotFound, "enrollment not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to rotate credential")
		return
	}
	result := enrollmentProjection(e)
	result["token"] = raw
	writeJSON(w, http.StatusOK, result)
}

func (h *Handler) RevokeDaemonEnrollment(w http.ResponseWriter, r *http.Request) {
	member, ok := h.enrollmentAdministrator(w, r)
	if !ok {
		return
	}
	id, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "enrollmentId"), "enrollment_id")
	if !ok {
		return
	}
	e, err := h.Queries.RevokeDaemonEnrollment(r.Context(), db.RevokeDaemonEnrollmentParams{ID: id, WorkspaceID: member.WorkspaceID})
	if isNotFound(err) {
		writeError(w, http.StatusNotFound, "enrollment not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to revoke credential")
		return
	}
	writeJSON(w, http.StatusOK, enrollmentProjection(e))
}
