package main

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
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

func seedLoginUser(t *testing.T, db *sql.DB, email, password, role string) {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	if err := UpsertUser(db, User{Email: email, DisplayName: email, Role: role}); err != nil {
		t.Fatal(err)
	}
	if err := SetUserPassword(db, email, string(hash)); err != nil {
		t.Fatal(err)
	}
}

// GET /login renders a password form; POST with correct credentials sets a
// session cookie, wrong password or unknown user is 401 with no cookie.
func TestLocalLogin(t *testing.T) {
	db := openTestDB(t)
	key := []byte("0123456789abcdef0123456789abcdef")
	seedLoginUser(t, db, "member1@example.com", "correct-horse", "member")
	h := HandleLocalLogin(db, key)

	req := httptest.NewRequest(http.MethodGet, "/login", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `type="password"`) {
		t.Fatalf("login form wrong: %d", rec.Code)
	}

	rec = postForm(t, h, "/login", url.Values{"email": {"member1@example.com"}, "password": {"correct-horse"}}, "")
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("good login: want 303, got %d", rec.Code)
	}
	var sess string
	for _, c := range rec.Result().Cookies() {
		if c.Name == "session" {
			sess = c.Value
		}
	}
	if sess == "" {
		t.Fatalf("session cookie missing")
	}
	if email, err := VerifySession(key, sess); err != nil || email != "member1@example.com" {
		t.Fatalf("session bad: %q %v", email, err)
	}

	rec = postForm(t, h, "/login", url.Values{"email": {"member1@example.com"}, "password": {"wrong"}}, "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("bad password: want 401, got %d", rec.Code)
	}
	rec = postForm(t, h, "/login", url.Values{"email": {"ghost@example.com"}, "password": {"x"}}, "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unknown user: want 401, got %d", rec.Code)
	}
}

// Password change needs the old password and rejects short new ones.
func TestPasswordChange(t *testing.T) {
	db := openTestDB(t)
	seedLoginUser(t, db, "member1@example.com", "old-pass-1", "member")
	h := HandlePasswordChange(db)

	rec := postForm(t, h, "/users/password", url.Values{"old": {"old-pass-1"}, "new": {"brand-new-2"}}, "member1@example.com")
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("change: want 303, got %d", rec.Code)
	}
	u, _ := GetUser(db, "member1@example.com")
	if err := bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte("brand-new-2")); err != nil {
		t.Fatalf("new password not set: %v", err)
	}
	rec = postForm(t, h, "/users/password", url.Values{"old": {"nope"}, "new": {"another-new"}}, "member1@example.com")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong old: want 401, got %d", rec.Code)
	}
	rec = postForm(t, h, "/users/password", url.Values{"old": {"brand-new-2"}, "new": {"short"}}, "member1@example.com")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("short new: want 400, got %d", rec.Code)
	}
}

// /connect stores the sealed mail secret and flips connected.
func TestSaveSecret(t *testing.T) {
	db := openTestDB(t)
	key := []byte("0123456789abcdef0123456789abcdef")
	seedLoginUser(t, db, "member1@example.com", "pw123456", "member")
	h := HandleSaveSecret(db, key, "Task Intake")

	rec := postForm(t, h, "/connect", url.Values{"secret": {"app-pass-xyz"}}, "member1@example.com")
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("save: want 303, got %d", rec.Code)
	}
	u, _ := GetUser(db, "member1@example.com")
	if !u.Connected || u.OutlookFolderID != "Task Intake" {
		t.Fatalf("not connected: %+v", u)
	}
	open, err := OpenToken(key, u.SecretEnc)
	if err != nil || string(open) != "app-pass-xyz" {
		t.Fatalf("secret wrong: %q %v", open, err)
	}
	rec = postForm(t, h, "/connect", url.Values{"secret": {""}}, "member1@example.com")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("empty secret: want 400, got %d", rec.Code)
	}
}

// Seed creates missing users with the default password, never resets existing.
func TestSeedUsers(t *testing.T) {
	db := openTestDB(t)
	if err := SeedUsers(db, "a@x.com:member,b@x.com:senior", "seed-pass"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a, err := GetUser(db, "a@x.com")
	if err != nil || a.Role != "member" {
		t.Fatalf("seeded member wrong: %+v %v", a, err)
	}
	if err := bcrypt.CompareHashAndPassword([]byte(a.PasswordHash), []byte("seed-pass")); err != nil {
		t.Fatalf("seed password wrong")
	}
	if _, err := GetUser(db, "b@x.com"); err != nil {
		t.Fatalf("senior missing: %v", err)
	}
	// Re-seed with a different password must not reset the existing one.
	if err := SeedUsers(db, "a@x.com:member", "other-pass"); err != nil {
		t.Fatal(err)
	}
	a, _ = GetUser(db, "a@x.com")
	if err := bcrypt.CompareHashAndPassword([]byte(a.PasswordHash), []byte("seed-pass")); err != nil {
		t.Fatalf("re-seed reset password")
	}
}

// Board gates read roles from the DB, not the environment.
func TestMemberGateDB(t *testing.T) {
	db := openTestDB(t)
	seedLoginUser(t, db, "member1@example.com", "pw123456", "member")
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	h := memberGate(db, "", inner)

	req := httptest.NewRequest(http.MethodPost, "/tasks", nil)
	req.Header.Set("X-User", "member1@example.com")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("member: want 200, got %d", rec.Code)
	}
	req = httptest.NewRequest(http.MethodPost, "/tasks", nil)
	req.Header.Set("X-User", "senior@example.com")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("non-member: want 403, got %d", rec.Code)
	}
}
