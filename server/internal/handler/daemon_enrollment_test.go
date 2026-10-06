package handler

import (
	"encoding/json"
	"strings"
	"testing"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestEnrollmentProjectionDoesNotExposeCredentialHash(t *testing.T) {
	raw, err := json.Marshal(enrollmentProjection(db.DaemonEnrollment{
		DaemonID: "host-one", Name: "Linux worker", TokenHash: "must-stay-private", Generation: 2,
	}))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "must-stay-private") || strings.Contains(string(raw), "token_hash") {
		t.Fatal("administrator projections must not expose credential hashes")
	}
	if !strings.Contains(string(raw), "host-one") || !strings.Contains(string(raw), "generation") {
		t.Fatal("administrator projections must expose host identity and credential version")
	}
}
