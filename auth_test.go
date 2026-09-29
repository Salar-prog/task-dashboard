package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestDisconnectClearsToken(t *testing.T) {
	db := openTestDB(t)
	key := []byte("0123456789abcdef0123456789abcdef")
	if err := UpsertUser(db, User{Email: "member1@example.com", Role: "member"}); err != nil {
		t.Fatal(err)
	}
	if err := SetUserConnected(db, "member1@example.com", []byte("sealed"), "folder-1"); err != nil {
		t.Fatal(err)
	}
	h := HandleDisconnect(db, key)
	c, _ := SignSession(key, "member1@example.com", time.Now().Add(time.Hour))
	req := httptest.NewRequest(http.MethodPost, "/disconnect", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: c})
	// WithUser normally fills X-User; the test sets it directly as the seam.
	req.Header.Set("X-User", "member1@example.com")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("want 303, got %d", rec.Code)
	}
	u, _ := GetUser(db, "member1@example.com")
	if u.Connected || len(u.RefreshTokenEnc) != 0 {
		t.Fatalf("token not wiped: %+v", u)
	}
}
