package middleware

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/multica-ai/multica/server/internal/auth"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// EnrollmentTxStarter is the database transaction boundary used to fence a
// daemon request against concurrent rotation/revocation. No auth cache is used.
type EnrollmentTxStarter interface {
	Begin(context.Context) (pgx.Tx, error)
}

type enrollmentContextKey int

const (
	enrollmentHashKey enrollmentContextKey = iota
	enrollmentOwnerKey
)

func DaemonEnrollmentHashFromContext(ctx context.Context) string {
	value, _ := ctx.Value(enrollmentHashKey).(string)
	return value
}
func DaemonEnrollmentOwnerFromContext(ctx context.Context) string {
	value, _ := ctx.Value(enrollmentOwnerKey).(string)
	return value
}

// serveEnrolledDaemon keeps the enrollment row locked during an HTTP operation.
// A WebSocket upgrade cannot hold a transaction for its connection lifetime:
// the handler re-authorizes every heartbeat using this same token hash instead.
func serveEnrolledDaemon(w http.ResponseWriter, r *http.Request, next http.Handler, queries *db.Queries, starter EnrollmentTxStarter, raw string) {
	if queries == nil || starter == nil {
		writeError(w, http.StatusServiceUnavailable, "daemon enrollment unavailable")
		return
	}
	tx, err := starter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "daemon enrollment unavailable")
		return
	}
	defer tx.Rollback(context.Background())
	enrollment, err := queries.WithTx(tx).LockLiveDaemonEnrollment(r.Context(), auth.HashToken(raw))
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusUnauthorized, "invalid daemon enrollment credential")
		return
	}
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "daemon enrollment unavailable")
		return
	}
	if !enrollment.ExpiresAt.Valid || !enrollment.ExpiresAt.Time.After(time.Now()) {
		writeError(w, http.StatusUnauthorized, "invalid daemon enrollment credential")
		return
	}
	ctx := context.WithValue(r.Context(), ctxKeyDaemonWorkspaceID, uuidToString(enrollment.WorkspaceID))
	ctx = context.WithValue(ctx, ctxKeyDaemonID, enrollment.DaemonID)
	ctx = context.WithValue(ctx, ctxKeyDaemonAuthPath, "daemon_enrollment")
	ctx = context.WithValue(ctx, enrollmentHashKey, auth.HashToken(raw))
	ctx = context.WithValue(ctx, enrollmentOwnerKey, uuidToString(enrollment.CreatorID))
	// Never propagate a caller-supplied user identity from a service credential.
	r.Header.Del("X-User-ID")
	if r.URL.Path == "/api/daemon/ws" {
		if err := tx.Commit(r.Context()); err != nil {
			writeError(w, http.StatusServiceUnavailable, "daemon enrollment unavailable")
			return
		}
	}
	next.ServeHTTP(w, r.WithContext(ctx))
	// No enrollment writes are made in this transaction. Rollback releases the
	// authorization lock only after the downstream operation has returned.
}
