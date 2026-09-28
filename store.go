package main

import (
	"database/sql"
	"fmt"
	"time"
)

// Task is the minimal board row pinned by docs/02-data-model.md.
type Task struct {
	ID           int64
	Title        string
	Desc         string
	Priority     string // low|medium|high|urgent
	Assigner     string
	Owner        string
	RoutineLabel string
	Recurrence   string // none|daily|weekly|monthly
	RelatedTo    string
	Status       string // todo|in_progress|blocked|done
	Progress     int
	Source       string // manual|teams|outlook
	SourceRef    string
	CreatedBy    string
	Deadline     time.Time // zero = none
}

// User is a login identity. Members store an encrypted Outlook refresh token;
// seniors store none (read-only, never polled).
type User struct {
	Email           string
	DisplayName     string
	Role            string // member|senior
	RefreshTokenEnc []byte
	OutlookFolderID string
	Connected       bool
}

// Migrate creates the tables (idempotent).
func Migrate(db *sql.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS tasks(
			id INTEGER PRIMARY KEY AUTOINCREMENT, title TEXT NOT NULL,
			desc TEXT NOT NULL DEFAULT '', priority TEXT NOT NULL DEFAULT 'medium',
			assigner TEXT NOT NULL DEFAULT '', owner TEXT NOT NULL DEFAULT '',
			deadline DATETIME, routine_label TEXT NOT NULL DEFAULT '',
			recurrence TEXT NOT NULL DEFAULT 'none', related_to TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'todo', progress_pct INTEGER NOT NULL DEFAULT 0,
			source TEXT NOT NULL DEFAULT 'manual', source_ref TEXT UNIQUE,
			created_by TEXT NOT NULL DEFAULT '',
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE INDEX IF NOT EXISTS idx_tasks_source_ref ON tasks(source_ref)`,
		`CREATE TABLE IF NOT EXISTS users(
			email TEXT PRIMARY KEY, display_name TEXT NOT NULL, role TEXT NOT NULL DEFAULT 'senior',
			refresh_token_enc BLOB, outlook_folder_id TEXT NOT NULL DEFAULT '',
			connected INTEGER NOT NULL DEFAULT 0,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS task_events(
			id INTEGER PRIMARY KEY AUTOINCREMENT, task_id INTEGER NOT NULL REFERENCES tasks(id),
			actor TEXT NOT NULL, from_status TEXT NOT NULL, to_status TEXT NOT NULL,
			note TEXT NOT NULL DEFAULT '', at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS import_errors(
			id INTEGER PRIMARY KEY AUTOINCREMENT, source TEXT NOT NULL,
			ref TEXT NOT NULL DEFAULT '', err TEXT NOT NULL,
			at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			return err
		}
	}
	return nil
}

func nullableTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t.UTC().Format(time.RFC3339)
}

// ImportTask inserts a Teams/Outlook task, or returns the existing id on source_ref.
// inserted=false on re-poll duplicates.
// ponytail: INSERT OR IGNORE keeps re-polls a no-op without a read-modify-write race.
func ImportTask(db *sql.DB, source, ref, title, assigner, owner, actor string) (id int64, inserted bool, err error) {
	res, err := db.Exec(
		`INSERT OR IGNORE INTO tasks(title, assigner, owner, source, source_ref, created_by)
		 VALUES(?,?,?,?,?,?)`, title, assigner, owner, source, ref, actor)
	if err != nil {
		return 0, false, err
	}
	if err := db.QueryRow(`SELECT id FROM tasks WHERE source_ref=?`, ref).Scan(&id); err != nil {
		return 0, false, err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return id, false, nil
	}
	if _, err := db.Exec(
		`INSERT INTO task_events(task_id, actor, from_status, to_status, note) VALUES(?, ?, '', 'todo', 'auto-import')`,
		id, actor); err != nil {
		return 0, false, err
	}
	return id, true, nil
}

// CreateTask inserts a manual/board task with sane defaults.
func CreateTask(db *sql.DB, task Task) (int64, error) {
	if task.Priority == "" {
		task.Priority = "medium"
	}
	if task.Status == "" {
		task.Status = "todo"
	}
	if task.Recurrence == "" {
		task.Recurrence = "none"
	}
	res, err := db.Exec(
		`INSERT INTO tasks(title, desc, priority, assigner, owner, deadline, routine_label,
		 recurrence, related_to, status, progress_pct, source, created_by)
		 VALUES(?,?,?,?,?,?,?,?,?,?,?, 'manual', ?)`,
		task.Title, task.Desc, task.Priority, task.Assigner, task.Owner,
		nullableTime(task.Deadline), task.RoutineLabel, task.Recurrence, task.RelatedTo,
		task.Status, task.Progress, task.CreatedBy)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	if _, err := db.Exec(
		`INSERT INTO task_events(task_id, actor, from_status, to_status, note) VALUES(?, '', '', ?, 'create')`,
		id, task.Status); err != nil {
		return 0, err
	}
	return id, nil
}

// UpsertUser inserts or refreshes identity/role on each login.
func UpsertUser(db *sql.DB, u User) error {
	_, err := db.Exec(
		`INSERT INTO users(email, display_name, role) VALUES(?,?,?)
		 ON CONFLICT(email) DO UPDATE SET display_name=excluded.display_name, role=excluded.role,
		 updated_at=CURRENT_TIMESTAMP`, u.Email, u.DisplayName, u.Role)
	return err
}

// GetUser returns sql.ErrNoRows for unknown emails.
func GetUser(db *sql.DB, email string) (User, error) {
	var u User
	var connected int
	err := db.QueryRow(
		`SELECT email, display_name, role, COALESCE(refresh_token_enc,''), COALESCE(outlook_folder_id,''), connected
		 FROM users WHERE email=?`, email).
		Scan(&u.Email, &u.DisplayName, &u.Role, &u.RefreshTokenEnc, &u.OutlookFolderID, &connected)
	if err != nil {
		return User{}, err
	}
	u.Connected = connected == 1
	return u, nil
}

// SetUserConnected caches the sealed token + resolved folder after consent.
func SetUserConnected(db *sql.DB, email string, sealedToken []byte, folderID string) error {
	_, err := db.Exec(
		`UPDATE users SET refresh_token_enc=?, outlook_folder_id=?, connected=1, updated_at=CURRENT_TIMESTAMP
		 WHERE email=?`, sealedToken, folderID, email)
	return err
}

func SetUserToken(db *sql.DB, email string, sealed []byte) error {
	_, err := db.Exec(`UPDATE users SET refresh_token_enc=?, updated_at=CURRENT_TIMESTAMP WHERE email=?`, sealed, email)
	return err
}

func ClearUserToken(db *sql.DB, email string) error {
	_, err := db.Exec(`UPDATE users SET refresh_token_enc=NULL, connected=0, updated_at=CURRENT_TIMESTAMP WHERE email=?`, email)
	return err
}

var recurrenceSteps = map[string]time.Duration{
	"daily":   24 * time.Hour,
	"weekly":  7 * 24 * time.Hour,
	"monthly": 30 * 24 * time.Hour,
}

// GetTask returns sql.ErrNoRows for unknown ids.
func GetTask(db *sql.DB, id int64) (Task, error) {
	var t Task
	var deadline sql.NullString
	err := db.QueryRow(
		`SELECT id, title, desc, priority, assigner, owner, deadline, routine_label,
		 recurrence, related_to, status, progress_pct, source, COALESCE(source_ref,''), created_by
		 FROM tasks WHERE id=?`, id).
		Scan(&t.ID, &t.Title, &t.Desc, &t.Priority, &t.Assigner, &t.Owner, &deadline,
			&t.RoutineLabel, &t.Recurrence, &t.RelatedTo, &t.Status, &t.Progress,
			&t.Source, &t.SourceRef, &t.CreatedBy)
	if err != nil {
		return Task{}, err
	}
	if deadline.Valid && deadline.String != "" {
		if d, err := time.Parse(time.RFC3339, deadline.String); err == nil {
			t.Deadline = d
		}
	}
	return t, nil
}

// UpdateTask writes the full row (callers overlay onto GetTask first).
func UpdateTask(db *sql.DB, t Task) error {
	res, err := db.Exec(
		`UPDATE tasks SET title=?, desc=?, priority=?, assigner=?, owner=?, deadline=?,
		 routine_label=?, recurrence=?, related_to=?, updated_at=CURRENT_TIMESTAMP WHERE id=?`,
		t.Title, t.Desc, t.Priority, t.Assigner, t.Owner, nullableTime(t.Deadline),
		t.RoutineLabel, t.Recurrence, t.RelatedTo, t.ID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// ListTasks returns tasks honoring owner/status/priority/q filters (empty = all).
func ListTasks(db *sql.DB, owner, status, priority, q string) ([]Task, error) {
	query := `SELECT id, title, desc, priority, assigner, owner, deadline, routine_label,
	 recurrence, related_to, status, progress_pct, source, COALESCE(source_ref,''), created_by
	 FROM tasks WHERE 1=1`
	var args []any
	if owner != "" {
		query += ` AND owner=?`
		args = append(args, owner)
	}
	if status != "" {
		query += ` AND status=?`
		args = append(args, status)
	}
	if priority != "" {
		query += ` AND priority=?`
		args = append(args, priority)
	}
	if q != "" {
		query += ` AND (title LIKE ? OR desc LIKE ? OR related_to LIKE ?)`
		like := "%" + q + "%"
		args = append(args, like, like, like)
	}
	query += ` ORDER BY id`
	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Task
	for rows.Next() {
		var t Task
		var deadline sql.NullString
		if err := rows.Scan(&t.ID, &t.Title, &t.Desc, &t.Priority, &t.Assigner, &t.Owner,
			&deadline, &t.RoutineLabel, &t.Recurrence, &t.RelatedTo, &t.Status, &t.Progress,
			&t.Source, &t.SourceRef, &t.CreatedBy); err != nil {
			return nil, err
		}
		if deadline.Valid && deadline.String != "" {
			if d, err := time.Parse(time.RFC3339, deadline.String); err == nil {
				t.Deadline = d
			}
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

var validStatuses = map[string]bool{"todo": true, "in_progress": true, "blocked": true, "done": true}

// UpdateProgress moves status + % together, logs history, and clones on done
// for recurring tasks (clone id returned via UpdateProgressDone).
func UpdateProgress(db *sql.DB, id int64, status string, pct int, note, actor string) error {
	_, err := updateProgress(db, id, status, pct, note, actor)
	return err
}

// UpdateProgressDone closes a task; returns the recurrence clone id (0 when none).
func UpdateProgressDone(db *sql.DB, id int64, actor string) (int64, error) {
	return updateProgress(db, id, "done", 100, "", actor)
}

func updateProgress(db *sql.DB, id int64, status string, pct int, note, actor string) (int64, error) {
	if !validStatuses[status] {
		return 0, fmt.Errorf("unknown status %q", status)
	}
	if pct < 0 || pct > 100 {
		return 0, fmt.Errorf("progress %d out of range", pct)
	}
	if status == "blocked" && note == "" {
		return 0, fmt.Errorf("blocked requires a reason note")
	}
	tx, err := db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	var recurrence, from string
	if err := tx.QueryRow(`SELECT recurrence, status FROM tasks WHERE id=?`, id).
		Scan(&recurrence, &from); err != nil {
		return 0, fmt.Errorf("task %d: %w", id, err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := tx.Exec(`UPDATE tasks SET status=?, progress_pct=?, updated_at=? WHERE id=?`,
		status, pct, now, id); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(
		`INSERT INTO task_events(task_id, actor, from_status, to_status, note) VALUES(?,?,?,?,?)`,
		id, actor, from, status, note); err != nil {
		return 0, err
	}
	if status != "done" {
		return 0, tx.Commit()
	}
	step, ok := recurrenceSteps[recurrence]
	if !ok {
		return 0, tx.Commit() // none or unknown: no clone
	}
	cloneID, err := cloneNext(tx, id, actor, time.Now().UTC().Add(step))
	if err != nil {
		return 0, err
	}
	return cloneID, tx.Commit()
}

// CompleteTask marks done and clones the next occurrence for recurring tasks.
// Returns 0 with no error when recurrence is none.
func CompleteTask(db *sql.DB, id int64, actor string) (int64, error) {
	return UpdateProgressDone(db, id, actor)
}

func cloneNext(tx *sql.Tx, id int64, actor string, next time.Time) (int64, error) {
	res, err := tx.Exec(
		`INSERT INTO tasks(title, desc, priority, assigner, owner, deadline, routine_label,
			recurrence, related_to, status, progress_pct, source, created_by)
		 SELECT title, desc, priority, assigner, owner, ?, routine_label,
			recurrence, related_to, 'todo', 0, source, ?
		 FROM tasks WHERE id=?`, next.Format(time.RFC3339), actor, id)
	if err != nil {
		return 0, err
	}
	cloneID, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	if _, err := tx.Exec(
		`INSERT INTO task_events(task_id, actor, from_status, to_status, note) VALUES(?, ?, '', 'todo', ?)`,
		cloneID, actor, fmt.Sprintf("recurrence clone of #%d", id)); err != nil {
		return 0, err
	}
	return cloneID, nil
}

func LogImportError(db *sql.DB, source, ref string, ierr error) error {
	_, err := db.Exec(`INSERT INTO import_errors(source, ref, err) VALUES(?,?,?)`, source, ref, ierr.Error())
	return err
}

func ListUsers(db *sql.DB) ([]User, error) {
	rows, err := db.Query(`SELECT email, display_name, role, COALESCE(refresh_token_enc,''), COALESCE(outlook_folder_id,''), connected FROM users ORDER BY email`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []User
	for rows.Next() {
		var u User
		var connected int
		if err := rows.Scan(&u.Email, &u.DisplayName, &u.Role, &u.RefreshTokenEnc, &u.OutlookFolderID, &connected); err != nil {
			return nil, err
		}
		u.Connected = connected == 1
		out = append(out, u)
	}
	return out, rows.Err()
}
