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

func TestDerivedEnrollmentFailsClosedWithoutAuthority(t *testing.T) {
	called := false
	req := httptest.NewRequest(http.MethodPost, "/api/daemon/tasks/claim", nil)
	req.Header.Set("Authorization", "Bearer mds_untrusted")
	req.Header.Set("X-User-ID", "forged-user")
	res := httptest.NewRecorder()
	DaemonAuth(nil, nil, nil, nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true })).ServeHTTP(res, req)
	if called || res.Code != http.StatusServiceUnavailable {
		t.Fatalf("derived enrollment must fail closed: status %d called %v", res.Code, called)
	}
}
