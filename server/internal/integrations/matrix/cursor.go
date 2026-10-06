package matrix

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

type cursorQueries interface {
	GetMatrixSyncCursor(context.Context, db.GetMatrixSyncCursorParams) (string, error)
	SaveMatrixSyncCursor(context.Context, db.SaveMatrixSyncCursorParams) error
}

// DBCursor isolates cursors across installations, homeservers/users and device
// resets. No token is logged or exposed through a public management response.
type DBCursor struct {
	q               cursorQueries
	installationID  pgtype.UUID
	appID, deviceID string
}

func NewDBCursor(q *db.Queries, installationID pgtype.UUID, config Config) CursorStore {
	return &DBCursor{q: q, installationID: installationID, appID: matrixAppID(config.Homeserver, config.UserID), deviceID: config.DeviceID}
}
func (s *DBCursor) Load(ctx context.Context) (string, error) {
	token, err := s.q.GetMatrixSyncCursor(ctx, db.GetMatrixSyncCursorParams{InstallationID: s.installationID, AppID: s.appID, DeviceID: s.deviceID})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return token, err
}
func (s *DBCursor) Save(ctx context.Context, token string) error {
	return s.q.SaveMatrixSyncCursor(ctx, db.SaveMatrixSyncCursorParams{InstallationID: s.installationID, AppID: s.appID, DeviceID: s.deviceID, SyncToken: token})
}
