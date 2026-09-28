package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGraphParsesMail(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if !strings.Contains(r.URL.Path, "folder-1/messages") {
			http.Error(w, "bad path "+r.URL.Path, http.StatusNotFound)
			return
		}
		w.Write([]byte(`{"value":[
			{"internetMessageId":"m1","subject":"Hi","from":{"emailAddress":{"address":"boss@example.com"}}},
			{"internetMessageId":"m2","subject":"","from":{"emailAddress":{"address":"p@example.com"}}}
		]}`))
	}))
	defer srv.Close()

	g := &GraphClient{BaseURL: srv.URL, HTTP: srv.Client()}
	mails, err := g.ListUnreadMail("tok", "folder-1")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(mails) != 2 || mails[0].ID != "m1" || mails[0].From != "boss@example.com" || mails[0].Subject != "Hi" {
		t.Fatalf("parsed wrong: %+v", mails)
	}
}
