package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
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

func TestResolveFolderNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"value":[]}`))
	}))
	defer srv.Close()
	g := &GraphClient{BaseURL: srv.URL, HTTP: srv.Client()}
	if _, err := g.ResolveFolderID("tok", "Task Intake"); !errors.Is(err, ErrFolderNotFound) {
		t.Fatalf("want ErrFolderNotFound, got %v", err)
	}
}

type fakeRefresher struct{ access string }

func (f fakeRefresher) Refresh(refresh string) (string, time.Time, error) {
	if refresh != "refresh-xyz" {
		return "", time.Time{}, errors.New("bad refresh")
	}
	return f.access, time.Now().Add(time.Hour), nil
}

func TestEnsureAccessTokenRefreshes(t *testing.T) {
	db := openTestDB(t)
	key := []byte("0123456789abcdef0123456789abcdef")
	if err := UpsertUser(db, User{Email: "member1@example.com", Role: "member"}); err != nil {
		t.Fatal(err)
	}
	sealed, _ := SealToken(key, []byte("refresh-xyz"))
	if err := SetUserToken(db, "member1@example.com", sealed); err != nil {
		t.Fatal(err)
	}
	got, err := EnsureAccessToken(db, key, fakeRefresher{access: "access-1"}, "member1@example.com")
	if err != nil || got != "access-1" {
		t.Fatalf("got %q %v", got, err)
	}
	if _, err := EnsureAccessToken(db, key, fakeRefresher{}, "ghost@example.com"); err == nil {
		t.Fatalf("unknown user must error")
	}
}

func TestAdaptersDrivePollCores(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "mailFolders/f1/messages"):
			w.Write([]byte(`{"value":[{"internetMessageId":"am1","subject":"Sign this","from":{"emailAddress":{"address":"boss@example.com"}}}]}`))
		case strings.Contains(r.URL.Path, "channels/c1/messages"):
			w.Write([]byte(`{"value":[{"id":"tm1","body":{"content":"Do the thing"},"from":{"user":{"displayName":"Boss"}},"mentions":[{"mentioned":{"user":{"userPrincipalName":"member1@example.com"}}}]}]}`))
		default:
			http.Error(w, "unexpected "+r.URL.Path, http.StatusNotFound)
		}
	}))
	defer srv.Close()

	db := openTestDB(t)
	if err := UpsertUser(db, User{Email: "member1@example.com", Role: "member"}); err != nil {
		t.Fatal(err)
	}
	if err := SetUserConnected(db, "member1@example.com", []byte("sealed"), "f1"); err != nil {
		t.Fatal(err)
	}
	g := &GraphClient{BaseURL: srv.URL, HTTP: srv.Client()}

	on, err := PollOutlookForUser(db, graphOutlook{g: g, token: "tok"}, "member1@example.com")
	if err != nil || on != 1 {
		t.Fatalf("outlook via adapter: got %d, %v", on, err)
	}
	tn, err := PollTeams(db, graphTeams{g: g, token: "tok"}, "t1", "c1", map[string]bool{"member1@example.com": true})
	if err != nil || tn != 1 {
		t.Fatalf("teams via adapter: got %d, %v", tn, err)
	}
	var owner string
	if err := db.QueryRow(`SELECT owner FROM tasks WHERE source_ref='tm1'`).Scan(&owner); err != nil {
		t.Fatalf("teams row: %v", err)
	}
	if owner != "member1@example.com" {
		t.Fatalf("adapter owner wrong: %q", owner)
	}
}
