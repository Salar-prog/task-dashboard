package main

import (
	"database/sql"
	"encoding/hex"
	"errors"
	"html/template"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

var validPriorities = map[string]bool{"low": true, "medium": true, "high": true, "urgent": true}
var validRecurrence = map[string]bool{"none": true, "daily": true, "weekly": true, "monthly": true}

func taskID(r *http.Request) (int64, error) {
	return strconv.ParseInt(r.PathValue("id"), 10, 64)
}

func writeErr(w http.ResponseWriter, err error) {
	if errors.Is(err, sql.ErrNoRows) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	http.Error(w, err.Error(), http.StatusBadRequest)
}

// HandleCreateTask accepts a prefilled board form; title is required.
func HandleCreateTask(db *sql.DB) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad form", http.StatusBadRequest)
			return
		}
		task := Task{
			Title:        r.FormValue("title"),
			Priority:     r.FormValue("priority"),
			Assigner:     r.FormValue("assigner"),
			Owner:        r.FormValue("owner"),
			RoutineLabel: r.FormValue("routine"),
			Recurrence:   r.FormValue("recurrence"),
			RelatedTo:    r.FormValue("related_to"),
			Desc:         r.FormValue("desc"),
			CreatedBy:    r.Header.Get("X-User"),
		}
		if task.Title == "" {
			http.Error(w, "title required", http.StatusBadRequest)
			return
		}
		if task.Priority == "" {
			task.Priority = "medium"
		}
		if !validPriorities[task.Priority] {
			http.Error(w, "bad priority", http.StatusBadRequest)
			return
		}
		if task.Recurrence == "" {
			task.Recurrence = "none"
		}
		if !validRecurrence[task.Recurrence] {
			http.Error(w, "bad recurrence", http.StatusBadRequest)
			return
		}
		if s := r.FormValue("deadline"); s != "" {
			d, err := time.Parse(time.RFC3339, s)
			if err != nil {
				http.Error(w, "bad deadline", http.StatusBadRequest)
				return
			}
			task.Deadline = d
		}
		if _, err := CreateTask(db, task); err != nil {
			writeErr(w, err)
			return
		}
		http.Redirect(w, r, "/", http.StatusSeeOther)
	})
}

// HandleUpdateTask overlays non-empty form values onto the stored row.
func HandleUpdateTask(db *sql.DB) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, err := taskID(r)
		if err != nil {
			http.Error(w, "bad id", http.StatusBadRequest)
			return
		}
		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad form", http.StatusBadRequest)
			return
		}
		task, err := GetTask(db, id)
		if err != nil {
			writeErr(w, err)
			return
		}
		if v := r.FormValue("title"); v != "" {
			task.Title = v
		}
		if v := r.FormValue("priority"); v != "" {
			if !validPriorities[v] {
				http.Error(w, "bad priority", http.StatusBadRequest)
				return
			}
			task.Priority = v
		}
		if v := r.FormValue("owner"); v != "" {
			task.Owner = v
		}
		if v := r.FormValue("assigner"); v != "" {
			task.Assigner = v
		}
		if v := r.FormValue("routine"); v != "" {
			task.RoutineLabel = v
		}
		if v := r.FormValue("recurrence"); v != "" {
			if !validRecurrence[v] {
				http.Error(w, "bad recurrence", http.StatusBadRequest)
				return
			}
			task.Recurrence = v
		}
		if v := r.FormValue("related_to"); v != "" {
			task.RelatedTo = v
		}
		if v := r.FormValue("desc"); v != "" {
			task.Desc = v
		}
		if s := r.FormValue("deadline"); s != "" {
			d, err := time.Parse(time.RFC3339, s)
			if err != nil {
				http.Error(w, "bad deadline", http.StatusBadRequest)
				return
			}
			task.Deadline = d
		}
		if err := UpdateTask(db, task); err != nil {
			writeErr(w, err)
			return
		}
		http.Redirect(w, r, "/", http.StatusSeeOther)
	})
}

// HandleHealth reports db liveness for container/proxy checks.
func HandleHealth(db *sql.DB) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := db.Ping(); err != nil {
			http.Error(w, "db down", http.StatusInternalServerError)
			return
		}
		w.Write([]byte("ok\n"))
	})
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func main() {
	db, err := OpenDB(getenv("DB_URL", "sqlite:"+getenv("DB_PATH", "/data/app.db")))
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()
	if err := Migrate(db); err != nil {
		log.Fatal(err)
	}
	if err := SeedUsers(db, getenv("SEED_USERS", ""), getenv("SEED_PASSWORD", "")); err != nil {
		log.Fatal(err)
	}
	tmpl, err := template.New("board").Funcs(BoardFuncs()).ParseFiles(filepath.Join(getenv("TEMPLATES_DIR", "templates"), "board.tmpl"))
	if err != nil {
		log.Fatal(err)
	}
	fallback := os.Getenv("MEMBER_TOKEN")
	tokenKey, err := hex.DecodeString(os.Getenv("TOKEN_KEY"))
	if err != nil || len(tokenKey) != 32 {
		log.Fatal("TOKEN_KEY must be 32-byte hex")
	}

	mux := http.NewServeMux()
	mux.Handle("GET /", boardWithRoles(db, tmpl))
	mux.Handle("GET /healthz", HandleHealth(db))
	mux.Handle("POST /tasks", memberGate(db, fallback, HandleCreateTask(db)))
	mux.Handle("POST /tasks/{id}/progress", memberGate(db, fallback, HandleTaskProgress(db)))
	mux.Handle("POST /tasks/{id}", memberGate(db, fallback, HandleUpdateTask(db)))
	mux.Handle("POST /disconnect", memberGate(db, fallback, HandleDisconnect(db, tokenKey)))
	mux.Handle("GET /logout", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "session", Value: "", Path: "/", MaxAge: -1})
		http.Redirect(w, r, "/", http.StatusSeeOther)
	}))
	mux.Handle("/login", HandleLocalLogin(db, tokenKey))
	mux.Handle("POST /users/password", memberGate(db, fallback, HandlePasswordChange(db)))
	mux.Handle("POST /connect", memberGate(db, fallback, HandleSaveSecret(db, tokenKey, getenv("WATCH_FOLDER_NAME", "Task Intake"))))

	// (The poll ticker returns with IMAP.)

	addr := ":" + getenv("PORT", "8080")
	log.Printf("listening on %s", addr)
	withUser := WithUser(tokenKey, mux)
	log.Fatal(http.ListenAndServe(addr, withUser))
}

// memberGate rebuilds the editor set from user roles on every request so
// role changes apply without restart; it delegates to RequireMember.
func memberGate(db *sql.DB, fallback string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		members := map[string]bool{}
		if users, err := ListUsers(db); err == nil {
			for _, u := range users {
				if u.Role == "member" {
					members[u.Email] = true
				}
			}
		}
		RequireMember(members, fallback, next).ServeHTTP(w, r)
	})
}

func boardWithRoles(db *sql.DB, tmpl *template.Template) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		members := map[string]bool{}
		if users, err := ListUsers(db); err == nil {
			for _, u := range users {
				if u.Role == "member" {
					members[u.Email] = true
				}
			}
		}
		HandleBoard(db, tmpl, members).ServeHTTP(w, r)
	})
}

// HandleTaskProgress moves status + % with validation (blocked needs note).
func HandleTaskProgress(db *sql.DB) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, err := taskID(r)
		if err != nil {
			http.Error(w, "bad id", http.StatusBadRequest)
			return
		}
		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad form", http.StatusBadRequest)
			return
		}
		pct, err := strconv.Atoi(r.FormValue("progress"))
		if err != nil {
			http.Error(w, "bad progress", http.StatusBadRequest)
			return
		}
		actor := r.Header.Get("X-User")
		if _, err := GetTask(db, id); err != nil {
			writeErr(w, err)
			return
		}
		if err := UpdateProgress(db, id, r.FormValue("status"), pct, r.FormValue("note"), actor); err != nil {
			writeErr(w, err)
			return
		}
		http.Redirect(w, r, "/", http.StatusSeeOther)
	})
}
