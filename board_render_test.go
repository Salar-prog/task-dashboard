package main

import (
	"database/sql"
	"html/template"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func boardHandler(t *testing.T, db *sql.DB) http.Handler {
	t.Helper()
	tmpl, err := template.ParseFiles("templates/board.tmpl")
	if err != nil {
		t.Fatalf("parse template: %v", err)
	}
	return HandleBoard(db, tmpl, testMembers)
}

// Board groups rows by owner, newest owners in first-seen order.
func TestBoardRendersGroups(t *testing.T) {
	db := openTestDB(t)
	mustCreate := func(title, owner string) {
		t.Helper()
		if _, err := CreateTask(db, Task{Title: title, Owner: owner}); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	mustCreate("Alpha task", "member1@example.com")
	mustCreate("Beta task", "member2@example.com")

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	boardHandler(t, db).ServeHTTP(rec, req)

	body := rec.Body.String()
	for _, want := range []string{"member1@example.com", "member2@example.com", "Alpha task", "Beta task"} {
		if !strings.Contains(body, want) {
			t.Fatalf("board missing %q", want)
		}
	}
}

// ?status=, ?priority= and ?q= narrow the rows.
func TestBoardFilters(t *testing.T) {
	db := openTestDB(t)
	if _, err := CreateTask(db, Task{Title: "Blocked deploy", Status: "blocked", Priority: "urgent", Owner: "m1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := CreateTask(db, Task{Title: "Calm docs", Status: "todo", Priority: "low", Owner: "m1"}); err != nil {
		t.Fatal(err)
	}

	get := func(q string) string {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/"+q, nil)
		rec := httptest.NewRecorder()
		boardHandler(t, db).ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s: want 200, got %d", q, rec.Code)
		}
		return rec.Body.String()
	}
	if body := get("?status=blocked"); !strings.Contains(body, "Blocked deploy") || strings.Contains(body, "Calm docs") {
		t.Fatalf("status filter wrong:\n%s", body)
	}
	if body := get("?priority=low"); !strings.Contains(body, "Calm docs") || strings.Contains(body, "Blocked deploy") {
		t.Fatalf("priority filter wrong")
	}
	if body := get("?q=deploy"); !strings.Contains(body, "Blocked deploy") || strings.Contains(body, "Calm docs") {
		t.Fatalf("q filter wrong")
	}
}

// Seniors/anon see zero forms; members get edit forms.
func TestBoardSeniorNoForms(t *testing.T) {
	db := openTestDB(t)
	if _, err := CreateTask(db, Task{Title: "T", Owner: "m1"}); err != nil {
		t.Fatal(err)
	}

	anon := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	boardHandler(t, db).ServeHTTP(rec, anon)
	if strings.Contains(strings.ToLower(rec.Body.String()), "<form") {
		t.Fatalf("anon board must contain no forms")
	}

	member := httptest.NewRequest(http.MethodGet, "/", nil)
	member.Header.Set("X-User", "member1@example.com")
	rec = httptest.NewRecorder()
	boardHandler(t, db).ServeHTTP(rec, member)
	if !strings.Contains(strings.ToLower(rec.Body.String()), "<form") {
		t.Fatalf("member board must contain edit forms")
	}
}
