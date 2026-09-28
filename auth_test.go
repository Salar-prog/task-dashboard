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

func TestLoginRedirect(t *testing.T) {
	h := HandleLogin("https://login.example/authorize?client_id=x")
	req := httptest.NewRequest(http.MethodGet, "/login", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusFound {
		t.Fatalf("want 302, got %d", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "https://login.example/authorize?client_id=x" {
		t.Fatalf("bad location %q", loc)
	}
}

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

func TestConnectResolvesFolder(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"value":[{"id":"fld-9"}]}`))
	}))
	defer srv.Close()

	db := openTestDB(t)
	key := []byte("0123456789abcdef0123456789abcdef")
	if err := UpsertUser(db, User{Email: "member1@example.com", Role: "member"}); err != nil {
		t.Fatal(err)
	}
	sealed, _ := SealToken(key, []byte("refresh-xyz"))
	if err := SetUserToken(db, "member1@example.com", sealed); err != nil {
		t.Fatal(err)
	}
	g := &GraphClient{BaseURL: srv.URL, HTTP: srv.Client()}
	h := HandleConnect(db, key, fakeRefresher{access: "a"}, g, "Task Intake")

	req := httptest.NewRequest(http.MethodGet, "/connect", nil)
	req.Header.Set("X-User", "member1@example.com")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("want 303, got %d", rec.Code)
	}
	u, _ := GetUser(db, "member1@example.com")
	if !u.Connected || u.OutlookFolderID != "fld-9" {
		t.Fatalf("not connected: %+v", u)
	}

	empty := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"value":[]}`))
	}))
	defer empty.Close()
	h2 := HandleConnect(db, key, fakeRefresher{access: "a"}, &GraphClient{BaseURL: empty.URL, HTTP: empty.Client()}, "Task Intake")
	rec = httptest.NewRecorder()
	h2.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("missing folder: want 400, got %d", rec.Code)
	}
}
