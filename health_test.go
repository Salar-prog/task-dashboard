package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// /healthz proves the HTTP layer + DB are alive (used by Docker/proxy checks).
func TestHealthz(t *testing.T) {
	db := openTestDB(t)

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	HandleHealth(db).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("healthz: want 200, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "ok") {
		t.Fatalf("healthz body must contain ok, got %q", rec.Body.String())
	}
}
