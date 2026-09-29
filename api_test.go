package main

import (
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

var testMembers = map[string]bool{"member1@example.com": true}

func testMux(db *sql.DB) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("POST /tasks", RequireMember(testMembers, "", HandleCreateTask(db)))
	mux.Handle("POST /tasks/{id}/progress", RequireMember(testMembers, "", HandleTaskProgress(db)))
	mux.Handle("POST /tasks/{id}", RequireMember(testMembers, "", HandleUpdateTask(db)))
	return mux
}

func postForm(t *testing.T, h http.Handler, path string, form url.Values, user string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if user != "" {
		req.Header.Set("X-User", user)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// Member creates → 303 + row; senior → 403; empty title → 400.
func TestCreateEndpoint(t *testing.T) {
	db := openTestDB(t)
	h := testMux(db)

	rec := postForm(t, h, "/tasks", url.Values{
		"title": {"New task"}, "owner": {"member1@example.com"}, "priority": {"high"},
	}, "member1@example.com")
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("create: want 303, got %d", rec.Code)
	}
	var title, priority string
	if err := db.QueryRow(`SELECT title, priority FROM tasks`).Scan(&title, &priority); err != nil {
		t.Fatalf("row: %v", err)
	}
	if title != "New task" || priority != "high" {
		t.Fatalf("row wrong: %q %q", title, priority)
	}

	rec = postForm(t, h, "/tasks", url.Values{"title": {"Sneaky"}}, "senior@example.com")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("senior create: want 403, got %d", rec.Code)
	}
	rec = postForm(t, h, "/tasks", url.Values{"title": {""}}, "member1@example.com")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("empty title: want 400, got %d", rec.Code)
	}
}

// Member edits fields → 303; bad priority → 400; unknown id → 404.
func TestEditEndpoint(t *testing.T) {
	db := openTestDB(t)
	h := testMux(db)

	id, err := CreateTask(db, Task{Title: "Old", Priority: "low", Owner: "member1@example.com"})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	path := fmt.Sprintf("/tasks/%d", id)
	rec := postForm(t, h, path, url.Values{"priority": {"urgent"}, "owner": {"member2@example.com"}}, "member1@example.com")
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("edit: want 303, got %d", rec.Code)
	}
	var priority, owner string
	db.QueryRow(`SELECT priority, owner FROM tasks WHERE id=?`, id).Scan(&priority, &owner)
	if priority != "urgent" || owner != "member2@example.com" {
		t.Fatalf("edit not applied: %q %q", priority, owner)
	}

	rec = postForm(t, h, path, url.Values{"priority": {"nonsense"}}, "member1@example.com")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad priority: want 400, got %d", rec.Code)
	}
	rec = postForm(t, h, "/tasks/9999", url.Values{"priority": {"high"}}, "member1@example.com")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown id: want 404, got %d", rec.Code)
	}
}

// Progress posts update status+% → 303; blocked without note → 400.
func TestProgressEndpoint(t *testing.T) {
	db := openTestDB(t)
	h := testMux(db)

	id, err := CreateTask(db, Task{Title: "Work", Owner: "member1@example.com"})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	path := fmt.Sprintf("/tasks/%d/progress", id)
	rec := postForm(t, h, path, url.Values{
		"status": {"in_progress"}, "progress": {"40"}, "note": {"started"},
	}, "member1@example.com")
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("progress: want 303, got %d body %q", rec.Code, rec.Body.String())
	}
	rec = postForm(t, h, path, url.Values{
		"status": {"blocked"}, "progress": {"40"}, "note": {""},
	}, "member1@example.com")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("blocked w/o note: want 400, got %d", rec.Code)
	}
}

func TestWithUserSetsHeader(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	var got string
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { got = r.Header.Get("X-User") })
	h := WithUser(key, next)

	c, _ := SignSession(key, "member1@example.com", time.Now().Add(time.Hour))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: c})
	httptest.NewRecorder()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if got != "member1@example.com" {
		t.Fatalf("want X-User set, got %q", got)
	}

	bad := httptest.NewRequest(http.MethodGet, "/", nil)
	got = "x"
	h.ServeHTTP(httptest.NewRecorder(), bad)
	if got != "" {
		t.Fatalf("invalid session must clear X-User, got %q", got)
	}
}
