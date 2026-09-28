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

func TestGraphParsesTeamsMentions(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"value":[
			{"id":"c1","body":{"content":"Hey <at>Ann</at> fix it"},"from":{"user":{"displayName":"Boss"}},
			 "mentions":[{"mentioned":{"user":{"userPrincipalName":"member1@example.com"}}}]},
			{"id":"c2","body":{"content":"chatter"},"from":{"user":{"displayName":"Boss"}}}
		]}`))
	}))
	defer srv.Close()

	g := &GraphClient{BaseURL: srv.URL, HTTP: srv.Client()}
	msgs, err := g.ListChannelMessages("tok", "t1", "c9")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("want 2, got %+v", msgs)
	}
	if len(msgs[0].Mentioned) != 1 || msgs[0].Mentioned[0] != "member1@example.com" {
		t.Fatalf("mentions wrong: %+v", msgs[0])
	}
	if msgs[0].From != "Boss" || !strings.Contains(msgs[0].Text, "fix it") {
		t.Fatalf("body wrong: %+v", msgs[0])
	}
	if len(msgs[1].Mentioned) != 0 {
		t.Fatalf("chatter must have no mentions")
	}
}
