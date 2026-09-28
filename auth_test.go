package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type fakeExchanger struct{ email, refresh string }

func (f fakeExchanger) Exchange(code string) (string, string, error) {
	if code != "good-code" {
		return "", "", errors.New("bad code")
	}
	return f.email, f.refresh, nil
}

func TestCallbackStoresMemberAndSession(t *testing.T) {
	db := openTestDB(t)
	key := []byte("0123456789abcdef0123456789abcdef")
	members := map[string]bool{"member1@example.com": true}
	ex := fakeExchanger{email: "member1@example.com", refresh: "refresh-xyz"}
	h := HandleCallback(db, key, members, ex)

	req := httptest.NewRequest(http.MethodGet, "/auth/callback?code=good-code", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("want 303, got %d", rec.Code)
	}
	u, err := GetUser(db, "member1@example.com")
	if err != nil {
		t.Fatalf("user row: %v", err)
	}
	if u.Role != "member" {
		t.Fatalf("role: got %q", u.Role)
	}
	open, err := OpenToken(key, u.RefreshTokenEnc)
	if err != nil || string(open) != "refresh-xyz" {
		t.Fatalf("sealed token wrong: %q %v", open, err)
	}
	if u.Connected {
		t.Fatalf("folder not yet resolved: connected must stay false")
	}
	found := false
	for _, c := range rec.Result().Cookies() {
		if c.Name == "session" {
			found = true
		}
	}
	if !found {
		t.Fatalf("session cookie missing")
	}

	bad := httptest.NewRequest(http.MethodGet, "/auth/callback?code=nope", nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, bad)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad code: want 400, got %d", rec.Code)
	}
}

var _ = time.Now
