package main

import (
	"database/sql"
	"encoding/hex"
	"errors"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"
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
	db, err := sql.Open("sqlite", getenv("DB_PATH", "/data/app.db"))
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()
	if err := Migrate(db); err != nil {
		log.Fatal(err)
	}
	tmpl, err := template.New("board").Funcs(BoardFuncs()).ParseFiles(filepath.Join(getenv("TEMPLATES_DIR", "templates"), "board.tmpl"))
	if err != nil {
		log.Fatal(err)
	}
	members := map[string]bool{}
	for _, e := range strings.Split(getenv("MEMBERS", ""), ",") {
		if e = strings.TrimSpace(e); e != "" {
			members[e] = true
		}
	}
	fallback := os.Getenv("MEMBER_TOKEN")
	tokenKey, err := hex.DecodeString(os.Getenv("TOKEN_KEY"))
	if err != nil || len(tokenKey) != 32 {
		log.Fatal("TOKEN_KEY must be 32-byte hex")
	}

	mux := http.NewServeMux()
	mux.Handle("GET /", HandleBoard(db, tmpl, members))
	mux.Handle("GET /healthz", HandleHealth(db))
	mux.Handle("POST /tasks", RequireMember(members, fallback, HandleCreateTask(db)))
	mux.Handle("POST /tasks/{id}/progress", RequireMember(members, fallback, HandleTaskProgress(db)))
	mux.Handle("POST /tasks/{id}", RequireMember(members, fallback, HandleUpdateTask(db)))
	loginURL := "https://login.microsoftonline.com/" + getenv("TENANT_ID", "common") +
		"/oauth2/v2.0/authorize?client_id=" + os.Getenv("CLIENT_ID") +
		"&response_type=code&redirect_uri=" + url.QueryEscape(getenv("REDIRECT_URL", "")) +
		"&scope=" + url.QueryEscape("openid profile email offline_access User.Read Mail.Read ChannelMessage.Read.All Team.ReadBasic.All")
	mux.Handle("GET /login", HandleLogin(loginURL))
	mux.Handle("GET /auth/callback", HandleCallback(db, tokenKey, members, oauthExchanger{config: oauthConfigFromEnv()}))
	mux.Handle("POST /disconnect", RequireMember(members, fallback, HandleDisconnect(db, tokenKey)))
	mux.Handle("GET /connect", RequireMember(members, fallback, HandleConnect(db, tokenKey, oauthRefresher{config: oauthConfigFromEnv()}, &GraphClient{}, getenv("WATCH_FOLDER_NAME", "Task Intake"))))
	mux.Handle("GET /logout", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "session", Value: "", Path: "/", MaxAge: -1})
		http.Redirect(w, r, "/", http.StatusSeeOther)
	}))

	refresher := oauthRefresher{config: oauthConfigFromEnv()}
	graphBase := &GraphClient{}
	teamID, channelID, resolved := "", "", false
	resolveIDs := func(tok string) (string, string) {
		if getenv("TEAM_NAMES", "") == "" {
			return getenv("TEAM_ID", ""), getenv("CHANNEL_ID", "")
		}
		teamName := strings.TrimSpace(strings.Split(getenv("TEAM_NAMES", ""), ",")[0])
		tid, err := graphBase.ResolveTeamID(tok, teamName)
		if err != nil {
			return getenv("TEAM_ID", ""), getenv("CHANNEL_ID", "")
		}
		chs, err := graphBase.ListChannels(tok, tid)
		if err != nil {
			return tid, getenv("CHANNEL_ID", "")
		}
		for _, c := range chs {
			for _, w := range strings.Split(getenv("CHANNEL_NAMES", ""), ",") {
				if strings.TrimSpace(w) != "" && c.Name == strings.TrimSpace(w) {
					return tid, c.ID
				}
			}
		}
		return tid, getenv("CHANNEL_ID", "")
	}
	go func() {
		interval, err := time.ParseDuration(getenv("POLL_EVERY", "7m"))
		if err != nil {
			interval = 7 * time.Minute
		}
		tick := time.NewTicker(interval)
		defer tick.Stop()
		for range tick.C {
			on, tn, err := RunPollCycle(db, tokenKey, refresher,
				func(tok string) OutlookSource { return graphOutlook{g: graphBase, token: tok} },
				func(tok string) (TeamsSource, string, string) {
					if !resolved {
						if t, c := resolveIDs(tok); t != "" {
							teamID, channelID, resolved = t, c, true
							log.Printf("poll resolved team=%s channel=%s", teamID, channelID)
						}
					}
					return graphTeams{g: graphBase, token: tok}, teamID, channelID
				})
			log.Printf("poll tick: outlook=%d teams=%d err=%v", on, tn, err)
		}
	}()

	addr := ":" + getenv("PORT", "8080")
	log.Printf("listening on %s", addr)
	withUser := WithUser(tokenKey, mux)
	log.Fatal(http.ListenAndServe(addr, withUser))
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
