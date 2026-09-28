package main

import (
	"strings"
	"testing"
)

// Progress and status move together, are logged, and blocked demands a reason.
// Done on a non-recurring task just closes it (no clone).
func TestProgressValidationAndHistory(t *testing.T) {
	db := openTestDB(t)

	id, err := CreateTask(db, Task{Title: "Ship it", Owner: "member1@example.com"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if err := UpdateProgress(db, id, "blocked", 40, "", "member1@example.com"); err == nil {
		t.Fatalf("blocked without note must fail")
	}
	if err := UpdateProgress(db, id, "bogus", 40, "x", "member1@example.com"); err == nil {
		t.Fatalf("unknown status must fail")
	}
	if err := UpdateProgress(db, id, "in_progress", 140, "x", "member1@example.com"); err == nil {
		t.Fatalf("progress >100 must fail")
	}

	if err := UpdateProgress(db, id, "in_progress", 40, "started", "member1@example.com"); err != nil {
		t.Fatalf("progress: %v", err)
	}
	var status string
	var pct int
	if err := db.QueryRow(`SELECT status, progress_pct FROM tasks WHERE id=?`, id).Scan(&status, &pct); err != nil {
		t.Fatalf("read: %v", err)
	}
	if status != "in_progress" || pct != 40 {
		t.Fatalf("want in_progress/40, got %s/%d", status, pct)
	}

	var note string
	err = db.QueryRow(`SELECT note FROM task_events WHERE task_id=? ORDER BY id DESC LIMIT 1`, id).Scan(&note)
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if !strings.Contains(note, "started") {
		t.Fatalf("history must carry note, got %q", note)
	}

	clone, err := UpdateProgressDone(db, id, "member1@example.com")
	if err != nil {
		t.Fatalf("done: %v", err)
	}
	if clone != 0 {
		t.Fatalf("non-recurring done must not clone, got %d", clone)
	}
}
