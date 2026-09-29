package main

import (
	"os"
	"testing"
)

// One SQL text must run on both drivers: ? on SQLite, $n on Postgres.
func TestRebindPlaceholders(t *testing.T) {
	SetDialectPG(false)
	if got := Q("SELECT id FROM tasks WHERE a=? AND b=?"); got != "SELECT id FROM tasks WHERE a=? AND b=?" {
		t.Fatalf("sqlite must pass through, got %q", got)
	}
	SetDialectPG(true)
	defer SetDialectPG(false)
	if got := Q("SELECT id FROM tasks WHERE a=? AND b=?"); got != "SELECT id FROM tasks WHERE a=$1 AND b=$2" {
		t.Fatalf("pg rebind wrong, got %q", got)
	}
	if got := Q("INSERT INTO t(a,b,c) VALUES(?,?,?)"); got != "INSERT INTO t(a,b,c) VALUES($1,$2,$3)" {
		t.Fatalf("pg rebind wrong, got %q", got)
	}
}

// Full CRUD round-trip on Postgres when a live server is provided.
// Proves every Q()-wrapped call site speaks valid Postgres.
func TestPostgresRoundTrip(t *testing.T) {
	dsn := os.Getenv("TEST_PG_DSN")
	if dsn == "" {
		t.Skip("no TEST_PG_DSN")
	}
	SetDialectPG(true)
	defer SetDialectPG(false)
	db, err := OpenDB(dsn)
	if err != nil {
		t.Fatalf("open pg: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec("DROP TABLE IF EXISTS task_events, tasks, users, import_errors"); err != nil {
		t.Fatalf("clean: %v", err)
	}
	if err := Migrate(db); err != nil {
		t.Fatalf("migrate pg: %v", err)
	}

	id, err := CreateTask(db, Task{Title: "pg task", Priority: "high", Owner: "m@x.com"})
	if err != nil || id == 0 {
		t.Fatalf("create pg: %d %v", id, err)
	}
	pid, inserted, err := ImportTask(db, "outlook", "pg-mail-1", "Subj", "b@x.com", "m@x.com", "sync:m@x.com")
	if err != nil || !inserted {
		t.Fatalf("import pg: %d %v %v", pid, inserted, err)
	}
	if _, _, err := ImportTask(db, "outlook", "pg-mail-1", "Subj", "b@x.com", "m@x.com", "sync:m@x.com"); err != nil {
		t.Fatalf("dedupe pg: %v", err)
	}
	if err := UpsertUser(db, User{Email: "m@x.com", DisplayName: "M", Role: "member"}); err != nil {
		t.Fatalf("upsert pg: %v", err)
	}
	if _, err := GetUser(db, "m@x.com"); err != nil {
		t.Fatalf("get pg: %v", err)
	}
	clone, err := UpdateProgressDone(db, id, "m@x.com")
	if err != nil || clone != 0 {
		t.Fatalf("done pg: %d %v", clone, err)
	}
	tasks, err := ListTasks(db, "", "", "", "")
	if err != nil || len(tasks) != 2 {
		t.Fatalf("list pg: %d %v", len(tasks), err)
	}
	evs, err := ListEvents(db, id, 10)
	if err != nil || len(evs) == 0 {
		t.Fatalf("events pg: %d %v", len(evs), err)
	}
}
