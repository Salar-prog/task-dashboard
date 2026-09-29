package main

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// test-only helper: in-memory DB. Production uses DB_PATH file.
func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open memdb: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := Migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func taskCount(t *testing.T, db *sql.DB) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM tasks`).Scan(&n); err != nil {
		t.Fatalf("count tasks: %v", err)
	}
	return n
}

// Same Outlook mail polled twice must yield exactly one task.
func TestOutlookDedupe(t *testing.T) {
	db := openTestDB(t)

	id1, _, err := ImportTask(db, "outlook", "AAMk-mail-1", "Invoice approval", "boss@example.com", "member1@example.com", "sync:member1@example.com")
	if err != nil {
		t.Fatalf("first import: %v", err)
	}
	id2, _, err := ImportTask(db, "outlook", "AAMk-mail-1", "Invoice approval", "boss@example.com", "member1@example.com", "sync:member1@example.com")
	if err != nil {
		t.Fatalf("second import: %v", err)
	}
	if id1 != id2 {
		t.Fatalf("dedupe: ids differ %d vs %d", id1, id2)
	}
	if n := taskCount(t, db); n != 1 {
		t.Fatalf("dedupe: want 1 task, got %d", n)
	}
}

// Completing a weekly task clones the next occurrence ~7d out, reset to todo/0%.
func TestRecurrenceClone(t *testing.T) {
	db := openTestDB(t)

	id, err := CreateTask(db, Task{
		Title: "Weekly report", Priority: "medium",
		Recurrence: "weekly", Status: "todo",
		Owner: "member1@example.com", Assigner: "boss@example.com",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	before := time.Now()
	cloneID, err := CompleteTask(db, id, "member1@example.com")
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if cloneID == 0 {
		t.Fatalf("want non-zero clone id for recurring task")
	}

	var status string
	var progress int
	var deadlineStr sql.NullString
	err = db.QueryRow(`SELECT status, progress_pct, deadline FROM tasks WHERE id=?`, cloneID).
		Scan(&status, &progress, &deadlineStr)
	if err != nil {
		t.Fatalf("read clone: %v", err)
	}
	deadline, err := time.Parse(time.RFC3339, deadlineStr.String)
	if err != nil {
		t.Fatalf("parse clone deadline %q: %v", deadlineStr.String, err)
	}
	if status != "todo" || progress != 0 {
		t.Fatalf("clone reset: want todo/0, got %s/%d", status, progress)
	}
	want := before.Add(7 * 24 * time.Hour)
	if deadline.Sub(want) > 5*time.Minute || want.Sub(deadline) > 5*time.Minute {
		t.Fatalf("clone deadline: want ~%v, got %v", want, deadline)
	}
}

// Seniors read (GET ok) but must not write (POST → 403); members pass.
func TestMemberGate(t *testing.T) {
	members := map[string]bool{"member1@example.com": true}
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	h := RequireMember(members, "dev-fallback", inner)

	// senior POST → 403
	req := httptest.NewRequest(http.MethodPost, "/tasks/1/progress", nil)
	req.Header.Set("X-User", "senior@example.com")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("senior POST: want 403, got %d", rec.Code)
	}

	// member POST → 200
	req = httptest.NewRequest(http.MethodPost, "/tasks/1/progress", nil)
	req.Header.Set("X-User", "member1@example.com")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("member POST: want 200, got %d", rec.Code)
	}

	// anonymous GET (senior view) → 200
	req = httptest.NewRequest(http.MethodGet, "/", nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("anon GET: want 200, got %d", rec.Code)
	}
}
