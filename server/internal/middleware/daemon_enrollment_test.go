package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDaemonEnrollmentFailsClosedWithoutAuthority(t *testing.T) {
	nextCalled := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { nextCalled = true })
	req := httptest.NewRequest(http.MethodPost, "/api/daemon/register", nil)
	req.Header.Set("Authorization", "Bearer mde_untrusted")
	req.Header.Set("X-Actor-Source", "human")
	req.Header.Set("X-Agent-ID", "forged-agent")
	res := httptest.NewRecorder()
	DaemonAuth(nil, nil, nil, nil)(next).ServeHTTP(res, req)
	if res.Code != http.StatusServiceUnavailable || nextCalled {
		t.Fatalf("unavailable enrollment authority must deny, status=%d next=%v", res.Code, nextCalled)
	}
	if req.Header.Get("X-Actor-Source") != "" || req.Header.Get("X-Agent-ID") != "" {
		t.Fatal("untrusted actor headers must be stripped before enrollment auth")
	}
}
