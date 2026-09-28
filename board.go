package main

import (
	"database/sql"
	"html/template"
	"net/http"
	"strings"
)

// OwnerGroup is one board section. BoardData drives templates/board.tmpl.
type OwnerGroup struct {
	Name  string
	Tasks []Task
}

type BoardData struct {
	Groups            []OwnerGroup
	Events            map[int64][]Event
	CanEdit           bool
	Owner, Status     string
	Priority, Q       string
	UnconnectedNotice string
}

// HandleBoard renders the grouped board. members maps verified login email → editor.
// Anyone without membership gets CanEdit=false: template omits every edit form.
func HandleBoard(db *sql.DB, tmpl *template.Template, members map[string]bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		tasks, err := ListTasks(db, q.Get("owner"), q.Get("status"), q.Get("priority"), q.Get("q"))
		if err != nil {
			http.Error(w, "board failed", http.StatusInternalServerError)
			return
		}
		var groups []OwnerGroup
		byOwner := map[string]int{}
		for _, t := range tasks {
			i, ok := byOwner[t.Owner]
			if !ok {
				groups = append(groups, OwnerGroup{Name: t.Owner})
				i = len(groups) - 1
				byOwner[t.Owner] = i
			}
			groups[i].Tasks = append(groups[i].Tasks, t)
		}
		data := BoardData{
			Groups:   groups,
			Events:   map[int64][]Event{},
			CanEdit:  members[r.Header.Get("X-User")],
			Owner:    q.Get("owner"),
			Status:   q.Get("status"),
			Priority: q.Get("priority"),
			Q:        q.Get("q"),
		}
		for _, t := range tasks {
			evs, err := ListEvents(db, t.ID, 5)
			if err != nil {
				http.Error(w, "board failed", http.StatusInternalServerError)
				return
			}
			data.Events[t.ID] = evs
		}
		if users, err := ListUsers(db); err == nil {
			var missing []string
			for _, u := range users {
				if u.Role == "member" && !u.Connected && members[u.Email] {
					missing = append(missing, u.Email)
				}
			}
			if len(missing) > 0 {
				data.UnconnectedNotice = "Outlook not connected: " + strings.Join(missing, ", ")
			}
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := tmpl.Execute(w, data); err != nil {
			http.Error(w, "render failed", http.StatusInternalServerError)
		}
	})
}
